package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wilamis-brasil/abraham/internal/command"
	"github.com/wilamis-brasil/abraham/internal/store"
)

func TestNormalSend(t *testing.T) {
	ctx := context.Background()
	d, f, db := testDispatcher(t)

	list := numbers(5)
	var res Result
	d.OnResult = func(r Result) { res = r }

	if err := d.Start(ctx, targets(list, "oi")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	d.Wait()

	// Sequential, and in the order the person wrote them.
	got := f.attempts()
	if len(got) != 5 {
		t.Fatalf("attempts = %d, want 5", len(got))
	}
	for i, n := range list {
		if got[i] != n {
			t.Errorf("position %d = %q, want %q", i, got[i], n)
		}
	}
	if res.Status != store.JobCompleted {
		t.Errorf("status = %q, want %q", res.Status, store.JobCompleted)
	}
	if res.Sent != 5 || res.Failed != 0 {
		t.Errorf("result = %d/%d, want 5/0", res.Sent, res.Failed)
	}
	if d.Running() {
		t.Error("still marked as running after finishing")
	}

	if failures, _ := db.Failures(ctx, res.JobID); len(failures) != 0 {
		t.Errorf("a clean send left %d failures", len(failures))
	}
}

func TestPartialFailureDoesNotStopTheSend(t *testing.T) {
	ctx := context.Background()
	d, f, db := testDispatcher(t)

	list := numbers(5)
	f.fail[list[1]] = errors.New("recipient not on whatsapp")
	f.fail[list[3]] = errors.New("timeout waiting for server")

	var res Result
	d.OnResult = func(r Result) { res = r }
	if err := d.Start(ctx, targets(list, "oi")); err != nil {
		t.Fatal(err)
	}
	d.Wait()

	// A failure does not interrupt the job: all five must have been attempted.
	if n := len(f.attempts()); n != 5 {
		t.Fatalf("attempts = %d, want 5 — one failure must not stop the send", n)
	}
	if res.Status != store.JobWithErrors {
		t.Errorf("status = %q, want %q", res.Status, store.JobWithErrors)
	}
	if res.Sent != 3 || res.Failed != 2 {
		t.Errorf("result = %d/%d, want 3/2", res.Sent, res.Failed)
	}

	failures, _ := db.Failures(ctx, res.JobID)
	if len(failures) != 2 {
		t.Fatalf("failures = %d, want 2", len(failures))
	}
	// The reason must be readable, not the raw technical error.
	if failures[0].Reason != "número não está no WhatsApp" {
		t.Errorf("reason = %q, want the Portuguese version", failures[0].Reason)
	}
	if strings.Contains(failures[1].Reason, "timeout waiting") {
		t.Errorf("the technical error leaked to the user: %q", failures[1].Reason)
	}
}

func TestNoRetry(t *testing.T) {
	ctx := context.Background()
	d, f, _ := testDispatcher(t)

	list := numbers(3)
	f.fail[list[0]] = errors.New("failed")
	if err := d.Start(ctx, targets(list, "oi")); err != nil {
		t.Fatal(err)
	}
	d.Wait()

	// Each number is attempted exactly once. Retrying on an ambiguous error
	// duplicates the message for a real person.
	times := map[string]int{}
	for _, n := range f.attempts() {
		times[n]++
	}
	for n, c := range times {
		if c != 1 {
			t.Errorf("%s was attempted %d times, want 1", n, c)
		}
	}
}

func TestCancelMidFlight(t *testing.T) {
	ctx := context.Background()
	d, f, db := testDispatcher(t)

	list := numbers(20)
	// Cancel as the third send begins.
	f.onSend = func(number string, n int) {
		if n == 2 {
			d.Cancel()
		}
	}

	var res Result
	d.OnResult = func(r Result) { res = r }
	if err := d.Start(ctx, targets(list, "oi")); err != nil {
		t.Fatal(err)
	}
	d.Wait()

	// The one already in flight finishes; the next never starts. So it stops at 3.
	if n := len(f.attempts()); n != 3 {
		t.Fatalf("attempts = %d, want 3 (the third finishes, the fourth never starts)", n)
	}
	if res.Status != store.JobCancelled {
		t.Errorf("status = %q, want %q", res.Status, store.JobCancelled)
	}
	if res.Sent != 3 {
		t.Errorf("sent = %d, want 3 — what already went out stays sent", res.Sent)
	}

	// The remaining 17 must show up in /falhas with a reason. If they stayed
	// pending, a restart would read them as a job to resume.
	failures, _ := db.Failures(ctx, res.JobID)
	if len(failures) != 17 {
		t.Fatalf("failures = %d, want 17", len(failures))
	}
	for _, f := range failures {
		if f.Reason == "" {
			t.Errorf("%s has no reason", f.Number)
		}
	}
	if left, _ := db.Pending(ctx, res.JobID); len(left) != 0 {
		t.Errorf("%d recipients left pending after cancelling", len(left))
	}
}

func TestCancelWithNothingRunning(t *testing.T) {
	d, _, _ := testDispatcher(t)
	if d.Cancel() {
		t.Error("Cancel returned true with no active send")
	}
}

func TestSecondSendIsRefused(t *testing.T) {
	ctx := context.Background()
	d, f, _ := testDispatcher(t)

	hold := make(chan struct{})
	f.onSend = func(string, int) { <-hold }

	if err := d.Start(ctx, targets(numbers(3), "a")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d.Running)

	if err := d.Start(ctx, targets(numbers(2), "b")); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start = %v, want ErrBusy", err)
	}

	close(hold)
	d.Wait()
}

func TestTemporaryBanStopsEverything(t *testing.T) {
	ctx := context.Background()
	d, f, _ := testDispatcher(t)

	hold := make(chan struct{})
	f.onSend = func(string, int) { <-hold }
	if err := d.Start(ctx, targets(numbers(10), "oi")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d.Running)

	d.Block("conta com restrição temporária")
	close(hold)
	d.Wait()

	// After a ban nothing is retried, not even with a fresh command.
	err := d.Start(ctx, targets(numbers(2), "oi"))
	if err == nil {
		t.Fatal("accepted a new send after the ban")
	}
	if !strings.Contains(err.Error(), "bloqueado") {
		t.Errorf("error = %q, want it to mention the block", err.Error())
	}
}

func TestConnectionDropInterrupts(t *testing.T) {
	ctx := context.Background()
	d, f, db := testDispatcher(t)
	// The real window is five minutes; here we only need to prove the drop leads
	// to an interruption and not to a silent partial send.
	f.onSend = func(number string, n int) {
		if n == 1 {
			d.SetOnline(false)
		}
	}

	var res Result
	d.OnResult = func(r Result) { res = r }
	if err := d.Start(ctx, targets(numbers(6), "oi")); err != nil {
		t.Fatal(err)
	}
	// Let the worker enter the wait, then cancel — which is what shutdown does.
	waitFor(t, func() bool { return len(f.attempts()) >= 2 })
	d.Cancel()
	d.Wait()

	if res.Sent != 2 {
		t.Errorf("sent = %d, want 2", res.Sent)
	}
	// The point: nobody is left pending. A surviving pending row becomes a
	// duplicate message on the next boot.
	if left, _ := db.Pending(ctx, res.JobID); len(left) != 0 {
		t.Errorf("%d recipients left pending after the drop", len(left))
	}
}

func TestEachRecipientGetsItsOwnText(t *testing.T) {
	ctx := context.Background()
	d, f, _ := testDispatcher(t)

	cmd, err := command.Parse(
		"/enviar\n55119000000001 Maria\n55119000000002 João\n55119000000003\n\nOlá {nome}, tudo bem?")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(ctx, cmd.Targets); err != nil {
		t.Fatal(err)
	}
	d.Wait()

	want := []string{"Olá Maria, tudo bem?", "Olá João, tudo bem?", "Olá, tudo bem?"}
	got := f.received()
	if len(got) != 3 {
		t.Fatalf("texts = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("recipient %d got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNamesNeverReachTheDatabase(t *testing.T) {
	// invariant 9: a recipient's name lives only in memory, like the message
	// text. None of it may survive the process.
	ctx := context.Background()
	d, _, db := testDispatcher(t)

	cmd, err := command.Parse("/enviar\n55119000000001 MariaDaSilvaTeste\n\nOlá {nome}")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(ctx, cmd.Targets); err != nil {
		t.Fatal(err)
	}
	d.Wait()

	var found int
	err = db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM app_recipients
		 WHERE numero LIKE '%MariaDaSilvaTeste%' OR motivo LIKE '%MariaDaSilvaTeste%'`).
		Scan(&found)
	if err != nil {
		t.Fatal(err)
	}
	if found != 0 {
		t.Error("the recipient's name ended up in the database")
	}
}

// ------------------------------------------------------------------ /falhas

func TestFailuresText(t *testing.T) {
	ctx := context.Background()
	d, f, _ := testDispatcher(t)

	list := numbers(4)
	f.fail[list[1]] = errors.New("recipient not on whatsapp")
	if err := d.Start(ctx, targets(list, "oi")); err != nil {
		t.Fatal(err)
	}
	d.Wait()

	msgs, err := d.FailuresText(ctx)
	if err != nil {
		t.Fatalf("FailuresText: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0], list[1]) {
		t.Errorf("the reply does not name the failed number: %q", msgs[0])
	}
	// The message text must never appear in an administrative reply.
	// (invariant 9)
	if strings.Contains(msgs[0], "oi") {
		t.Errorf("the message content leaked into /falhas: %q", msgs[0])
	}
}

func TestFailuresAreChunked(t *testing.T) {
	ctx := context.Background()
	d, f, _ := testDispatcher(t)

	list := numbers(250)
	for _, n := range list {
		f.fail[n] = errors.New("failed")
	}
	if err := d.Start(ctx, targets(list, "oi")); err != nil {
		t.Fatal(err)
	}
	d.Wait()

	msgs, err := d.FailuresText(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 250 failures in chunks of 100 = 3 messages.
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	total := 0
	for _, m := range msgs {
		for _, l := range strings.Split(m, "\n") {
			if strings.HasPrefix(l, "5511") {
				total++
			}
		}
	}
	if total != 250 {
		t.Errorf("numbers listed = %d, want 250", total)
	}
}

func TestFailuresWithNoSendYet(t *testing.T) {
	d, _, _ := testDispatcher(t)
	msgs, err := d.FailuresText(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0], "Nenhum envio") {
		t.Errorf("reply = %#v", msgs)
	}
}
