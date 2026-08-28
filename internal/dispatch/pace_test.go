package dispatch

import (
	"context"
	"testing"
	"time"
)

func TestIntervalAlwaysFallsInRange(t *testing.T) {
	d := &Dispatcher{MinInterval: time.Second, MaxInterval: 7 * time.Second}

	var sum time.Duration
	const samples = 500
	for i := 0; i < samples; i++ {
		w := d.drawInterval()
		if w < time.Second || w > 7*time.Second {
			t.Fatalf("drew %v, outside the 1s-7s range", w)
		}
		sum += w
	}
	// The average is what actually dilutes the send. If it drifts towards one
	// end, the draw is biased and the pacing is no longer what was designed.
	avg := sum / samples
	if avg < 3*time.Second || avg > 5*time.Second {
		t.Errorf("average = %v, expected close to 4s", avg)
	}
}

func TestIntervalActuallyVaries(t *testing.T) {
	// If every draw came back the same, the cadence would be exact again —
	// which is precisely the pattern this is meant to break.
	d := &Dispatcher{MinInterval: time.Second, MaxInterval: 7 * time.Second}
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		seen[d.drawInterval()] = true
	}
	if len(seen) < 10 {
		t.Errorf("only %d distinct values in 50 draws — it is not varying", len(seen))
	}
}

func TestZeroRangeMeansNoWait(t *testing.T) {
	d := &Dispatcher{}
	if w := d.drawInterval(); w != 0 {
		t.Errorf("with a zero range the wait should be 0, got %v", w)
	}
}

func TestCancelDoesNotWaitOutTheInterval(t *testing.T) {
	// Without this, a /cancelar could take up to 7 seconds to take effect, and
	// the person would send it again thinking it did not work.
	d := &Dispatcher{MinInterval: 5 * time.Second, MaxInterval: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())

	start := time.Now()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if d.sleep(ctx) {
		t.Error("sleep returned true after being cancelled")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v to wake up — should be immediate", elapsed)
	}
}

func TestEstimatedDuration(t *testing.T) {
	d := &Dispatcher{MinInterval: time.Second, MaxInterval: 7 * time.Second}
	// 500 recipients at a 4s average = 499 waits ~ 33 min.
	got := d.EstimatedDuration(500)
	if got < 30*time.Minute || got > 36*time.Minute {
		t.Errorf("EstimatedDuration(500) = %v, expected close to 33min", got)
	}
	if d.EstimatedDuration(1) != 0 {
		t.Error("a single recipient has no wait at all")
	}
}
