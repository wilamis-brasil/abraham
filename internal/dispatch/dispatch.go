// Package dispatch owns the one send job that may exist at a time.
//
// Sequential, concurrency 1, one number at a time, with a randomised wait
// between them. No pool, no goroutine per recipient, no retry.
//
// That is not naivety: every one of those missing things is a whole category of
// problem that does not exist here. With concurrency 1, cancelling is trivial,
// the database state never goes ambiguous through a race, and the log tells the
// story in the order it happened.
package dispatch

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wilamis-brasil/abraham/internal/command"
	"github.com/wilamis-brasil/abraham/internal/store"
)

// Sender is the only thing this package needs from WhatsApp.
//
// One method, declared here in the consumer, satisfied implicitly by the real
// client. That is what lets the whole dispatcher be tested without an account —
// and the reason it stays this small: a wider interface would be a wider fake.
type Sender interface {
	Send(ctx context.Context, number, text string) error
}

const (
	// sendTimeout caps a single WhatsApp call. Without it, one hung call would
	// stall the entire job without ever failing.
	sendTimeout = 30 * time.Second

	// reconnectWindow is how long a send waits out a temporary drop before
	// giving up. After that the job is interrupted and the remaining numbers go
	// to /falhas.
	reconnectWindow = 5 * time.Minute

	// pollInterval is how often the worker checks whether the connection came
	// back.
	pollInterval = 500 * time.Millisecond
)

// ErrBusy comes back when a /enviar arrives while another one is running.
var ErrBusy = errors.New("já existe um envio em andamento")

// Progress is what the console needs in order to draw. One per recipient.
type Progress struct {
	JobID  int64
	Total  int
	Done   int
	Sent   int
	Failed int
	Number string // the recipient of this step
	Status string // store.StatusSending, store.StatusSent or store.StatusFailed
	Reason string

	// Remaining is how much longer the job should take. The dispatcher owns the
	// pacing, so it is the one that can say — the console just draws it, and
	// never needs to know an interval exists.
	Remaining time.Duration
}

// Result closes a job.
type Result struct {
	JobID    int64
	Status   string // store.JobCompleted, JobWithErrors, JobCancelled or JobInterrupted
	Total    int
	Sent     int
	Failed   int
	Duration time.Duration
}

// Dispatcher controls the single active send.
type Dispatcher struct {
	db     *store.DB
	sender Sender

	// Wait range between recipients. Fields rather than constants only so the
	// tests do not take seconds per number.
	MinInterval, MaxInterval time.Duration

	// OnProgress and OnResult are where the console plugs in. Both may be nil:
	// this package does not know what a screen is. (invariant 11)
	OnProgress func(Progress)
	OnResult   func(Result)

	mu      sync.Mutex
	active  *activeJob
	done    sync.WaitGroup
	online  bool
	blocked string // reason for a hard stop, such as a temporary ban
}

type activeJob struct {
	id     int64
	cancel context.CancelFunc
}

// New builds the dispatcher. It starts out assuming there is a connection:
// whoever sent a /enviar just wrote it over WhatsApp.
func New(db *store.DB, sender Sender) *Dispatcher {
	return &Dispatcher{
		db:          db,
		sender:      sender,
		MinInterval: defaultMinInterval,
		MaxInterval: defaultMaxInterval,
		online:      true,
	}
}

// SetOnline tells the dispatcher the connection dropped or came back.
func (d *Dispatcher) SetOnline(online bool) {
	d.mu.Lock()
	d.online = online
	d.mu.Unlock()
}

// Block stops everything and refuses new sends. Used when WhatsApp reports a
// temporary ban: the right answer is to stop, never to work around it.
func (d *Dispatcher) Block(reason string) {
	d.mu.Lock()
	d.blocked = reason
	active := d.active
	d.mu.Unlock()
	if active != nil {
		active.cancel()
	}
}

// Running reports whether a send is in flight right now.
func (d *Dispatcher) Running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active != nil
}

// Wait blocks until there is no active send. Used on shutdown and in tests.
func (d *Dispatcher) Wait() { d.done.Wait() }

// Start checks availability and runs the send in the background.
//
// The texts arrive ready from the parser and live only in this goroutine's
// memory. None of that reaches the database or the log. (invariant 9)
func (d *Dispatcher) Start(ctx context.Context, targets []command.Target) error {
	d.mu.Lock()
	if d.blocked != "" {
		reason := d.blocked
		d.mu.Unlock()
		return errors.New("envio bloqueado: " + reason)
	}
	if d.active != nil {
		d.mu.Unlock()
		return ErrBusy
	}
	// Reserve the slot before touching the database, so two simultaneous
	// /enviar cannot create two jobs.
	d.active = &activeJob{}
	d.mu.Unlock()

	numbers := make([]string, len(targets))
	for i, t := range targets {
		numbers[i] = t.Number
	}
	id, err := d.db.CreateJob(ctx, numbers)
	if err != nil {
		d.mu.Lock()
		d.active = nil
		d.mu.Unlock()
		return err
	}

	// The job context governs cancellation BETWEEN recipients. It is not passed
	// to the send itself — see run().
	jobCtx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.active.id = id
	d.active.cancel = cancel
	d.mu.Unlock()

	d.done.Add(1)
	go func() {
		defer d.done.Done()
		defer cancel()
		d.run(jobCtx, id, targets)
	}()
	return nil
}

// Cancel stops the active send. Returns false if nothing was running.
//
// There is no confirmation step: whoever typed /cancelar wants it to stop now.
func (d *Dispatcher) Cancel() bool {
	d.mu.Lock()
	active := d.active
	d.mu.Unlock()
	if active == nil {
		return false
	}
	active.cancel()
	return true
}

func (d *Dispatcher) run(ctx context.Context, id int64, targets []command.Target) {
	start := time.Now()
	pending, err := d.db.Pending(ctx, id)
	if err != nil {
		d.finish(id, store.JobInterrupted, start)
		return
	}

	// Each recipient's text came ready from the parser, with {nome} already
	// substituted. Here we only need to find the right one by number.
	text := make(map[string]string, len(targets))
	for _, t := range targets {
		text[t.Number] = t.Text
	}

	final := store.JobCompleted
	for _, recipient := range pending {
		// Cancellation is cooperative and checked HERE, before the next number.
		// What already went out, went out: there is no un-sending.
		if ctx.Err() != nil {
			final = store.JobCancelled
			break
		}
		if !d.waitForConnection(ctx) {
			final = store.JobInterrupted
			break
		}

		d.report(id, recipient, store.StatusSending, "")
		_ = d.db.MarkRecipient(ctx, recipient.ID, store.StatusSending, "")

		// Its own context, detached from cancellation: a /cancelar must not
		// abort the message already in flight. Aborting mid-call would leave the
		// outcome ambiguous — exactly what the two-step marking exists to avoid.
		sendCtx, cancelSend := context.WithTimeout(context.Background(), sendTimeout)
		sendErr := d.sender.Send(sendCtx, recipient.Number, text[recipient.Number])
		cancelSend()

		status, reason := store.StatusSent, ""
		if sendErr != nil {
			status, reason = store.StatusFailed, describeError(sendErr)
			final = store.JobWithErrors
		}
		// Background context: even when cancelled, the outcome of what already
		// went out has to be written down.
		_ = d.db.MarkRecipient(context.Background(), recipient.ID, status, reason)
		d.report(id, recipient, status, reason)

		if !d.sleep(ctx) {
			final = store.JobCancelled
			break
		}
	}

	d.finish(id, final, start)
}

// waitForConnection holds the send during a temporary drop. Returns false if
// the connection did not come back inside the window, or if the job was
// cancelled while waiting.
func (d *Dispatcher) waitForConnection(ctx context.Context) bool {
	deadline := time.Now().Add(reconnectWindow)
	for {
		d.mu.Lock()
		online := d.online
		d.mu.Unlock()
		if online {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pollInterval):
		}
	}
}

func (d *Dispatcher) finish(id int64, status string, start time.Time) {
	ctx := context.Background()

	// A cancelled or interrupted job leaves numbers behind. They have to show up
	// in /falhas with a reason that explains what happened — and, just as
	// importantly, they must not stay `pending`, or the next boot would read
	// them as a job to resume.
	if status == store.JobCancelled || status == store.JobInterrupted {
		reason := "o envio foi cancelado antes de chegar neste número"
		if status == store.JobInterrupted {
			reason = "a conexão com o WhatsApp caiu durante o envio"
		}
		if left, err := d.db.Pending(ctx, id); err == nil {
			for _, r := range left {
				_ = d.db.MarkRecipient(ctx, r.ID, store.StatusNotSent, reason)
			}
		}
	}
	_ = d.db.FinishJob(ctx, id, status)

	d.mu.Lock()
	d.active = nil
	d.mu.Unlock()

	res := Result{JobID: id, Status: status, Duration: time.Since(start)}
	if j, err := d.db.LastJob(ctx); err == nil && j != nil && j.ID == id {
		res.Total, res.Sent, res.Failed = j.Total, j.Sent, j.Failed
	}
	if d.OnResult != nil {
		d.OnResult(res)
	}
}

func (d *Dispatcher) report(id int64, r store.Recipient, status, reason string) {
	if d.OnProgress == nil {
		return
	}
	j, err := d.db.LastJob(context.Background())
	p := Progress{JobID: id, Number: r.Number, Status: status, Reason: reason}
	if err == nil && j != nil {
		p.Total, p.Sent, p.Failed = j.Total, j.Sent, j.Failed
		p.Done = j.Sent + j.Failed
		p.Remaining = d.EstimatedDuration(p.Total - p.Done + 1)
	}
	d.OnProgress(p)
}
