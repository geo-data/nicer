package main

import (
	"bufio"
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/c9s/goprocinfo/linux"
)

type SampleHandler func(metric float32, err error)

type Sampler interface {
	Sample(ctx context.Context, interval time.Duration, cb SampleHandler)
}

type SampleFunc func() (float32, error)

func (f SampleFunc) Sample(ctx context.Context, interval time.Duration, cb SampleHandler) {
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			cb(f())
		case <-ctx.Done():
			// fmt.Println(ctx.Err()) // prints "context deadline exceeded"
			return
		}
	}

	return
}

var (
	MemorySampler Sampler = SampleFunc(func() (percent float32, err error) {
		var mi *linux.MemInfo
		if mi, err = linux.ReadMemInfo("/proc/meminfo"); err != nil {
			return
		}

		free := mi.MemFree + mi.Cached + mi.Buffers
		percent = (float32(free) / float32(mi.MemTotal)) * 100.0
		return
	})

	LoadAvg1MinSampler Sampler = SampleFunc(func() (load float32, err error) {
		var l *linux.LoadAvg
		if l, err = linux.ReadLoadAvg("/proc/loadavg"); err != nil {
			return
		}

		load = float32(l.Last1Min)
		return
	})
)

func getStats() (*linux.CPUStat, error) {
	if stat, err := linux.ReadStat("/proc/stat"); err != nil {
		return nil, err
	} else {
		return &stat.CPUStatAll, nil
	}
}

type CPUSampler struct {
	prevStat *linux.CPUStat
}

func NewCPUSampler() *CPUSampler {
	return &CPUSampler{}
}

func (s *CPUSampler) Init() (err error) {
	s.prevStat, err = getStats()
	return
}

func (s *CPUSampler) Measure() (percent float32, err error) {
	var curStat *linux.CPUStat
	curStat, err = getStats()
	if err != nil {
		s.prevStat = nil
		return
	}

	defer func() {
		s.prevStat = curStat // Update the previous value with the current one.
	}()

	if s.prevStat == nil {
		err = errors.New("no previous cpu statistics available")
		return
	}

	// Calculate the percentage total CPU usage (adapted from
	// <http://stackoverflow.com/questions/23367857/accurate-calculation-of-cpu-usage-given-in-percentage-in-linux>).
	prevIdle := s.prevStat.Idle + s.prevStat.IOWait
	idle := curStat.Idle + curStat.IOWait

	prevNonIdle := s.prevStat.User + s.prevStat.Nice + s.prevStat.System + s.prevStat.IRQ + s.prevStat.SoftIRQ + s.prevStat.Steal
	nonIdle := curStat.User + curStat.Nice + curStat.System + curStat.IRQ + curStat.SoftIRQ + curStat.Steal

	prevTotal := prevIdle + prevNonIdle
	total := idle + nonIdle

	// Differentiate: actual values minus the previous one
	totald := total - prevTotal
	idled := idle - prevIdle

	percent = (float32(totald-idled) / float32(totald)) * 100
	return
}

func (s *CPUSampler) Sample(ctx context.Context, interval time.Duration, cb SampleHandler) {
	t := time.NewTicker(interval)
	defer t.Stop()

	if err := s.Init(); err != nil {
		cb(0, err)
	}

	for {
		select {
		case <-t.C:
			cb(s.Measure())
		case <-ctx.Done():
			// fmt.Println(ctx.Err()) // prints "context deadline exceeded"
			return
		}
	}

	return
}

type Threshold struct {
	Name      string  // The identifier for this threshold.
	LastValue float32 // Last usage value
	Threshold float32 // Percentage threshold
	Sampler   Sampler // Sample source.
}

type AlertHandler func(name string, value float32, exceeded bool)

type ErrorHandler func(err error)

func NewThreshold(name string, sampler Sampler, threshold float32) *Threshold {
	return &Threshold{
		Name:      name,
		Sampler:   sampler,
		Threshold: threshold,
	}
}

func (t *Threshold) Poll(ctx context.Context, interval time.Duration, alert AlertHandler, eh ErrorHandler) {
	handler := func(metric float32, err error) {
		if err != nil {
			eh(err)
			return
		}

		// See if we need to send an alert.
		if t.LastValue < t.Threshold && metric >= t.Threshold {
			alert(t.Name, metric, true)
		} else if t.LastValue >= t.Threshold && metric < t.Threshold {
			alert(t.Name, metric, false)
		}
		t.LastValue = metric
	}

	// Set the initial value as the threshold.
	handler(t.Threshold, nil)

	go t.Sampler.Sample(ctx, interval, handler)
	return
}

type ThresholdGroup struct {
	sync.RWMutex
	Thresholds []*Threshold
	exceeded   uint8
	wait       chan struct{}
}

func NewThresholdGroup(thresholds ...*Threshold) *ThresholdGroup {
	return &ThresholdGroup{Thresholds: thresholds}
}

func (t *ThresholdGroup) updateExceeded(exceeded bool) {
	t.Lock()
	defer t.Unlock()

	if exceeded {
		t.exceeded++
		if t.exceeded == 1 {
			t.wait = make(chan struct{})
		}
	} else {
		t.exceeded--
		if t.exceeded == 0 {
			close(t.wait)
			t.wait = nil
		}
	}

	return
}

func (t *ThresholdGroup) Exceeded() bool {
	t.RLock()
	defer t.RUnlock()
	return t.exceeded != 0
}

func (t *ThresholdGroup) Wait() {
	select {
	case _, ok := <-t.wait:
		if !ok {
			return
		}
	}
}

func (t *ThresholdGroup) Poll(ctx context.Context, interval time.Duration, errh ErrorHandler) {
	alert := func(name string, value float32, exceeded bool) {
		log.Printf("%s %v %v", name, value, exceeded)
		t.updateExceeded(exceeded)
	}

	for _, ct := range t.Thresholds {
		if ct == nil {
			continue
		}

		ct.Poll(ctx, interval, alert, errh)
	}
}

func main() {
	interval := time.Second * 1
	t := NewThresholdGroup(
		NewThreshold("cpu", NewCPUSampler(), 90),
		NewThreshold("ram", MemorySampler, 90),
		NewThreshold("load", LoadAvg1MinSampler, 5),
	)

	t.Poll(context.Background(), interval, func(err error) {
		log.Println(err)
	})

	var wg sync.WaitGroup

	scanner := bufio.NewScanner(os.Stdin)
	i := 0
	for scanner.Scan() {
		i++
		wg.Add(1)
		time.Sleep(interval)
		cmdText := scanner.Text()

		if t.Exceeded() {
			log.Printf("%d: waiting", i)
			t.Wait()
		}

		log.Printf("Command %d executing: %s", i, cmdText)
		go func(index int) {
			defer wg.Done()
			cmd := exec.Command("sh", "-c", cmdText)
			if err := cmd.Run(); err != nil {
				log.Printf("Command %d failed: %s", index, err)
			} else {
				log.Printf("Command %d succeeded", index)
			}
		}(i)
	}
	if err := scanner.Err(); err != nil {
		log.Println("reading standard input:", err)
	}

	wg.Wait()
}
