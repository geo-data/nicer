package sample

import (
	"context"
	"errors"
	"time"

	"github.com/c9s/goprocinfo/linux"
)

// CPU defines a Sampler which measures percentage CPU usage.
type CPU struct {
	// prevStat caches the last measurement.
	prevStat *linux.CPUStat
}

// NewCPU instantiates a CPU.
func NewCPU() *CPU {
	return &CPU{}
}

// Init initialises the sampler by obtaining and cachine an initial measurement.
func (s *CPU) Init() (err error) {
	s.prevStat, err = getStats()
	return
}

// Measure calculates the percentage CPU usage by comparing a new measurement
// against the previous measurement.
func (s *CPU) Measure() (percent float32, err error) {
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
func (s *CPU) Sample(ctx context.Context, interval time.Duration, cb Handler) {
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

// getStats returns the linux.CPUStat for all CPUs.
func getStats() (*linux.CPUStat, error) {
	stat, err := linux.ReadStat("/proc/stat")
	if err != nil {
		return nil, err
	}
	return &stat.CPUStatAll, nil
}
