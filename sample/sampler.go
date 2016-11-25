// Package sample provides an interface and implementations for sampling system
// resource metrics.
package sample

import (
	"context"
	"time"

	"github.com/c9s/goprocinfo/linux"
)

type (
	// Handler represents a handler that is called every time a metric is
	// sampled.
	Handler func(metric float32, err error)

	// Sampler defines the interface to be implemented for sampling metrics. The
	// Sample method should not return until the context is cancelled.
	Sampler interface {
		Sample(ctx context.Context, interval time.Duration, cb Handler)
	}

	// Func enhances functions matching the signature to conform to the Sampler
	// interface.
	Func func() (float32, error)
)

// Sample implements the Sampler interface and will call f whenever a metric
// needs to be sampled.  Sample will not return until the context is cancelled.
func (f Func) Sample(ctx context.Context, interval time.Duration, cb Handler) {
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
	// Memory defines a Sampler that obtains the percentage of free memory as
	// calculated from /proc/meminfo.
	Memory Sampler = Func(func() (percent float32, err error) {
		var mi *linux.MemInfo
		if mi, err = linux.ReadMemInfo("/proc/meminfo"); err != nil {
			return
		}

		free := mi.MemFree + mi.Cached + mi.Buffers
		percent = (float32(free) / float32(mi.MemTotal)) * 100.0
		return
	})

	// LoadAvg1Min defines a Sampler that obtains the average system load as
	// calculated for the last minute from /proc/loadavg.
	LoadAvg1Min Sampler = Func(func() (load float32, err error) {
		var l *linux.LoadAvg
		if l, err = linux.ReadLoadAvg("/proc/loadavg"); err != nil {
			return
		}

		load = float32(l.Last1Min)
		return
	})
)
