package threshold

import (
	"context"
	"sync"
	"time"
)

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
