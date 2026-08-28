package dispatch

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wilamis-brasil/abraham/internal/command"
	"github.com/wilamis-brasil/abraham/internal/store"
)

// fakeSender is a WhatsApp that never leaves the process.
//
// It is what makes the whole dispatcher testable without an account: normal
// send, partial failure, cancellation mid-flight, connection drop. None of
// those could be reproduced on purpose against the real WhatsApp.
type fakeSender struct {
	mu sync.Mutex

	numbers []string // in order, so tests can check the sequence
	texts   []string // what each recipient actually received

	fail   map[string]error // per-number programmed failure
	delay  time.Duration
	onSend func(number string, n int) // hook: cancel, drop the connection, block
}

func newFake() *fakeSender { return &fakeSender{fail: map[string]error{}} }

func (f *fakeSender) Send(ctx context.Context, number, text string) error {
	f.mu.Lock()
	n := len(f.numbers)
	hook := f.onSend
	delay := f.delay
	err := f.fail[number]
	f.numbers = append(f.numbers, number)
	f.texts = append(f.texts, text)
	f.mu.Unlock()

	if hook != nil {
		hook(number, n)
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return err
}

func (f *fakeSender) attempts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.numbers...)
}

func (f *fakeSender) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

var _ Sender = (*fakeSender)(nil)

// testDispatcher wires store + fake + dispatcher with no wait between numbers.
// The 1-7s range is production; in a test it would only make the suite slow
// without proving anything. The pacing has its own tests in pace_test.go.
func testDispatcher(t *testing.T) (*Dispatcher, *fakeSender, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "dados", "abraham.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	f := newFake()
	d := New(db, f)
	d.MinInterval, d.MaxInterval = 0, 0
	return d, f, db
}

// numbers builds n valid phone numbers.
func numbers(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("55119%08d", i)
	}
	return out
}

// targets builds recipients that all share the same text, which is the case for
// most dispatch tests: what is under test here is pacing and cancellation, not
// personalisation — that has its tests in the command package.
func targets(nums []string, text string) []command.Target {
	out := make([]command.Target, len(nums))
	for i, n := range nums {
		out[i] = command.Target{Number: n, Text: text}
	}
	return out
}

// waitFor polls a condition. Avoids a fixed sleep, which makes the suite slow
// and flaky at the same time.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the condition never became true within 3s")
}
