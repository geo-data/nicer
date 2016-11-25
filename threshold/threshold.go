// Package threshold defines types for setting and monitoring a threshold or
// group of thresholds against sampled system metrics.
package threshold

import (
	"context"
	"time"

	"github.com/geo-data/nicer/sample"
)

type (
	// Threshold combines a Sampler with a threshold value and arbitrary name.
	// It can then poll the Sampler and compare metrics against the threshold,
	// triggering alerts when a threshold is crossed.
	Threshold struct {
		Name      string         // The identifier for this threshold.
		Threshold float32        // The threshold value.
		Sampler   sample.Sampler // Sample source.
		Ascending bool           // Whether the treshold is based on increasing values.
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
func NewThreshold(name string, sampler sample.Sampler, threshold float32, ascending bool) *Threshold {
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
