package main

import (
	"log"
	"os/exec"
	"time"

	"github.com/c9s/goprocinfo/linux"
)

type Metricer interface {
	Metric() (float32, error)
}

type MetricFunc func() (float32, error)

func (f MetricFunc) Metric() (float32, error) {
	return f()
}

var MemoryMetric Metricer = MetricFunc(func() (percent float32, err error) {
	var mi *linux.MemInfo
	if mi, err = linux.ReadMemInfo("/proc/meminfo"); err != nil {
		return
	}

	free := mi.MemFree + mi.Cached + mi.Buffers
	percent = (float32(free) / float32(mi.MemTotal)) * 100.0
	return
})

func getStats() (*linux.CPUStat, error) {
	if stat, err := linux.ReadStat("/proc/stat"); err != nil {
		return nil, err
	} else {
		return &stat.CPUStatAll, nil
	}
}

type CPUMetric struct {
	sampleTime time.Duration
}

func NewCPUMetric(sampleTime time.Duration) *CPUMetric {
	return &CPUMetric{sampleTime}
}

func (u *CPUMetric) Metric() (percent float32, err error) {
	var pstat, cstat *linux.CPUStat

	if pstat, err = getStats(); err != nil {
		return
	}

	time.Sleep(u.sampleTime)

	if cstat, err = getStats(); err != nil {
		return
	}

	// Calculate the percentage total CPU usage (adapted from
	// <http://stackoverflow.com/questions/23367857/accurate-calculation-of-cpu-usage-given-in-percentage-in-linux>).
	prevIdle := pstat.Idle + pstat.IOWait
	idle := cstat.Idle + cstat.IOWait

	prevNonIdle := pstat.User + pstat.Nice + pstat.System + pstat.IRQ + pstat.SoftIRQ + pstat.Steal
	nonIdle := cstat.User + cstat.Nice + cstat.System + cstat.IRQ + cstat.SoftIRQ + cstat.Steal

	prevTotal := prevIdle + prevNonIdle
	total := idle + nonIdle

	// Differentiate: actual values minus the previous one
	totald := total - prevTotal
	idled := idle - prevIdle

	percent = (float32(totald-idled) / float32(totald)) * 100
	return
}

type Threshold struct {
	LastValue float32  // Last usage value
	Threshold float32  // Percentage threshold
	Metric    Metricer // Source to be checked
	Err       error    // Errors encountered
}

func NewThreshold(metric Metricer, threshold float32) *Threshold {
	return &Threshold{
		Threshold: threshold,
		Metric:    metric,
	}
}

func (t *Threshold) Exceeded() bool {
	u, err := t.Metric.Metric()
	if err != nil {
		t.Err = err
		return true
	}

	t.LastValue = u
	return t.LastValue > t.Threshold
}

/*type Check struct {
	Thresholds []
}*/

func main() {
	thresholds := []*Threshold{
		NewThreshold(NewCPUMetric(1*time.Second), 75),
		NewThreshold(MemoryMetric, 60),
	}

	for i := 0; i < 20; i++ {

	Loop:
		for {
			for j, t := range thresholds {
				if t.Exceeded() {
					log.Printf("%d usage = %f%%: waiting", j, t.LastValue)
					continue Loop
				}
			}

			break Loop // No thresholds are exceeded.
		}

		log.Printf("Command %d executing", i)
		go func(index int) {
			cmd := exec.Command("sh", "-c", "timeout 10s yes > /dev/null")
			if err := cmd.Run(); err != nil {
				log.Printf("Command %d failed: %s", index, err)
			} else {
				log.Printf("Command %d succeeded", index)
			}
		}(i)
	}

	// Wait for goroutines to finish.
	select {}
}
