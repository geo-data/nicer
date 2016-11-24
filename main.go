package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/c9s/goprocinfo/linux"
)

// These get set on build.
var version, commit string

// init checks and optionally initialises the version strings.
func init() {
	if version == "" {
		version = "No version information,"
	}
	if commit == "" {
		commit = "unknown"
	}
}

// SampleHandler represents a handler that is called every time a metric is
// sampled.
type SampleHandler func(metric float32, err error)

// Sampler defines the interface to be implemented for sampling metrics. The
// Sample method should not return until the context is cancelled.
type Sampler interface {
	Sample(ctx context.Context, interval time.Duration, cb SampleHandler)
}

// SampleFunc enhances functions matching the signature to conform to the
// Sampler interface.
type SampleFunc func() (float32, error)

// Sample implements the Sampler interface and will call f whenever a metric
// needs to be sampled.  Sample will not return until the context is cancelled.
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
}

var (
	// MemorySampler defines a Sampler that obtains the percentage of free
	// memory as calculated from /proc/meminfo.
	MemorySampler Sampler = SampleFunc(func() (percent float32, err error) {
		var mi *linux.MemInfo
		if mi, err = linux.ReadMemInfo("/proc/meminfo"); err != nil {
			return
		}

		free := mi.MemFree + mi.Cached + mi.Buffers
		percent = (float32(free) / float32(mi.MemTotal)) * 100.0
		return
	})

	// LoadAvg1MinSampler defines a Sampler that obtains the average system load
	// as calculated for the last minute from /proc/loadavg.
	LoadAvg1MinSampler Sampler = SampleFunc(func() (load float32, err error) {
		var l *linux.LoadAvg
		if l, err = linux.ReadLoadAvg("/proc/loadavg"); err != nil {
			return
		}

		load = float32(l.Last1Min)
		return
	})
)

// getStats returns the linux.CPUStat for all CPUs.
func getStats() (*linux.CPUStat, error) {
	stat, err := linux.ReadStat("/proc/stat")
	if err != nil {
		return nil, err
	}
	return &stat.CPUStatAll, nil
}

// CPUSampler defines a Sampler which measures percentage CPU usage.
type CPUSampler struct {
	// prevStat caches the last measurement.
	prevStat *linux.CPUStat
}

// NewCPUSampler instantiates a CPUSampler.
func NewCPUSampler() *CPUSampler {
	return &CPUSampler{}
}

// Init initialises the sampler by obtaining and cachine an initial measurement.
func (s *CPUSampler) Init() (err error) {
	s.prevStat, err = getStats()
	return
}

// Measure calculates the percentage CPU usage by comparing a new measurement
// against the previous measurement.
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

// Sample implements the Sampler interface to perform CPU usage measurements.
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
}

type (
	// Threshold combines a Sampler with a threshold value and arbitrary name.
	// It can then poll the Sampler and compare metrics against the threshold,
	// triggering alerts when a threshold is crossed.
	Threshold struct {
		Name      string  // The identifier for this threshold.
		Threshold float32 // The threshold value.
		Sampler   Sampler // Sample source.
		Ascending bool    // Whether the treshold is based on increasing values.
	}

	// AlertHandler represents a function that is called whenever a threshold
	// value is crossed.  If the threshold is exceeded, exceeded will be true or
	// false otherwise.
	AlertHandler func(name string, value float32, exceeded bool)

	// ErrorHandler represents a function that is called whenever a Sampler
	// encounters an error.
	ErrorHandler func(name string, err error)
)

// NewThreshold instantiates a Threshold.
func NewThreshold(name string, sampler Sampler, threshold float32, ascending bool) *Threshold {
	return &Threshold{
		Name:      name,
		Threshold: threshold,
		Sampler:   sampler,
		Ascending: ascending,
	}
}

// Poll samples the Threshold Sampler every interval.  If the threshold value is
// crossed, AlertHandler is called.  If the sampler encounters an error,
// ErrorHandler is called. ctx is passed to the Sampler. Poll does not block.
func (t *Threshold) Poll(ctx context.Context, interval time.Duration, alert AlertHandler, eh ErrorHandler) {
	var (
		sendAlert func(float32)
		lastValue float32 = t.Threshold // Last sample value
	)

	if t.Ascending {
		sendAlert = func(metric float32) {
			if lastValue < t.Threshold && metric >= t.Threshold {
				alert(t.Name, metric, true)
			} else if lastValue >= t.Threshold && metric < t.Threshold {
				alert(t.Name, metric, false)
			}
		}
	} else {
		sendAlert = func(metric float32) {
			if lastValue > t.Threshold && metric <= t.Threshold {
				alert(t.Name, metric, true)
			} else if lastValue <= t.Threshold && metric > t.Threshold {
				alert(t.Name, metric, false)
			}
		}
	}

	handler := func(metric float32, err error) {
		if err != nil {
			eh(t.Name, err)
			return
		}

		// Send an alert if required.
		sendAlert(metric)
		lastValue = metric
	}

	// Send an initial alert
	alert(t.Name, t.Threshold, true)

	go t.Sampler.Sample(ctx, interval, handler)
	return
}

// ThresholdGroup represents a collection of Threshold instances.  It is used to
// determine whether any thresholds have been exceeded, and to wait until no
// thresholds is exceeded.
type ThresholdGroup struct {
	sync.RWMutex
	Thresholds []*Threshold
	exceeded   uint8
	wait       chan struct{}
}

// NewThresholdGroup instantiates a ThresholdGroup.
func NewThresholdGroup(thresholds ...*Threshold) *ThresholdGroup {
	return &ThresholdGroup{Thresholds: thresholds}
}

// updateExceeded updates the internal reference count of exceeded thresholds.
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

// Exceeded returns true if any thresholds have been exceeded, false otherwise.
func (t *ThresholdGroup) Exceeded() bool {
	t.RLock()
	defer t.RUnlock()
	return t.exceeded != 0
}

// Wait blocks until no thresholds are exceeded.
func (t *ThresholdGroup) Wait() {
	select {
	case _, ok := <-t.wait:
		if !ok {
			return
		}
	}
}

// Poll delegates to the Poll methods of all the Threshold instances managed by t.  It does not block.
func (t *ThresholdGroup) Poll(ctx context.Context, interval time.Duration, alert AlertHandler, errh ErrorHandler) {
	alertWrap := func(name string, value float32, exceeded bool) {
		t.updateExceeded(exceeded)
		alert(name, value, exceeded)
	}

	for _, ct := range t.Thresholds {
		if ct == nil {
			continue
		}

		ct.Poll(ctx, interval, alertWrap, errh)
	}
}

func scan(s *bufio.Scanner, w io.Writer, pid int) {
	for s.Scan() {
		fmt.Fprintf(w, "nicer %d: %s\n", pid, s.Text())
	}
}

func startCommand(ctx context.Context, cmdStr string) (cmd *exec.Cmd, stdout, stderr io.ReadCloser, err error) {
	cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	if stdout, err = cmd.StdoutPipe(); err != nil {
		return
	}
	if stderr, err = cmd.StderrPipe(); err != nil {
		return
	}
	if err = cmd.Start(); err != nil {
		return
	}
	return
}

func catchSignals(cancel context.CancelFunc) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		for sig := range c {
			log.Printf("received %s signal", sig.String())
			cancel()
		}
	}()
}

func main() {
	var (
		tcpu       = flag.Float64("cpu-threshold", 90, "percentage of cpu usage above which commands will not be executed")
		tram       = flag.Float64("ram-threshold", 10, "percentage of free memory below which commands will not be executed")
		tload      = flag.Float64("load-threshold", (90.0/100.0)*float64(runtime.NumCPU()), "1 minute load average above which commands will not be executed")
		interval   = flag.Duration("interval", time.Second*1, "sampling interval for resource metrics")
		wait       = flag.Duration("wait", time.Second*1, "duration to wait between issuing commands. Used when there is no wait on resource thresholds")
		cin        = flag.String("input", "", "file location to read commands from. Defaults to STDIN.")
		cout       = flag.String("stdout", "", "file location to send command standard output to. Defaults to STDOUT.")
		cerr       = flag.String("stderr", "", "file location to send command standard error to. Defaults to STDERR.")
		v          = flag.Bool("v", false, "print version information and exit.")
		fout, ferr io.WriteCloser
		fin        io.Reader
	)

	flag.Parse()

	if *v {
		fmt.Printf("%s commit=%s\n", version, commit)
		os.Exit(0)
	}

	if len(*cin) > 0 {
		fh, err := os.Open(*cin)
		if err != nil {
			log.Fatal(err)
		}
		defer fh.Close()
		fin = fh
	} else {
		fin = os.Stdin
	}

	if len(*cout) > 0 {
		fh, err := os.Create(*cout)
		if err != nil {
			log.Fatal(err)
		}
		defer fh.Close()
		fout = fh
	} else {
		fout = os.Stdout
	}

	if len(*cerr) > 0 {
		if *cerr == *cout {
			ferr = fout
		} else {
			fh, err := os.Create(*cerr)
			if err != nil {
				log.Fatal(err)
			}
			defer fh.Close()
			ferr = fh
		}
	} else {
		ferr = os.Stderr
	}

	t := NewThresholdGroup(
		NewThreshold("cpu", NewCPUSampler(), float32(*tcpu), true),
		NewThreshold("ram", MemorySampler, float32(*tram), false),
		NewThreshold("load", LoadAvg1MinSampler, float32(*tload), true),
	)

	ctx, cancel := context.WithCancel(context.Background())
	catchSignals(cancel)

	t.Poll(ctx, *interval,
		func(name string, value float32, exceeded bool) {
			if exceeded {
				log.Printf("%s threshold exceeded: %v", name, value)
			} else {
				log.Printf("%s threshold ok: %v", name, value)
			}
		},
		func(name string, err error) {
			log.Println(name, err)
		},
	)

	scanner := bufio.NewScanner(fin)
	commands := make(chan string, 5)
	go func() {
		defer close(commands)

		for scanner.Scan() {
			cmd := scanner.Text()
			if len(cmd) > 0 {
				commands <- cmd
			}
		}
		if err := scanner.Err(); err != nil {
			log.Println("reading standard input:", err)
		}
	}()

	runCommands(ctx, commands, t, *wait, fout, ferr)
}

func runCommands(
	ctx context.Context,
	commands <-chan string,
	t *ThresholdGroup,
	wait time.Duration,
	fout, ferr io.Writer,
) {
	var wg sync.WaitGroup

	i := 0
Process:
	for {
		select {
		case <-ctx.Done():
			break Process
		case cmdText, ok := <-commands:
			if !ok {
				break Process
			}

			i++
			if t.Exceeded() {
				log.Println("waiting")
				t.Wait()
			} else if i > 1 {
				time.Sleep(wait)
				if t.Exceeded() {
					log.Println("waiting")
					t.Wait()
				}
			}

			wg.Add(1)
			go func(index int) {
				defer wg.Done()

				cmd, stdout, stderr, err := startCommand(ctx, cmdText)
				if err != nil {
					log.Println("failed to start command", cmdText, err)
					return
				}

				pid := cmd.Process.Pid
				log.Printf("nicer %d executing pid %d: %s", index, pid, cmdText)

				scanOut := bufio.NewScanner(stdout)
				scanErr := bufio.NewScanner(stderr)

				var swg sync.WaitGroup
				swg.Add(2)
				go func() {
					defer swg.Done()
					scan(scanOut, fout, pid)
				}()
				go func() {
					defer swg.Done()
					scan(scanErr, ferr, pid)
				}()
				swg.Wait()
				if err := scanOut.Err(); err != nil {
					log.Println("scanning stdout for pid", pid, err)
				}
				if err := scanErr.Err(); err != nil {
					log.Println("scanning stderr for pid", pid, err)
				}

				if err := cmd.Wait(); err != nil {
					log.Printf("command pid %d failed: %s", pid, err)
				} else {
					log.Printf("command pid %d succeeded", pid)
				}

			}(i)
		}
	}

	wg.Wait()
}
