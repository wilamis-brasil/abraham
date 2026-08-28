package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "dados", "abraham.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestNormalJobLifecycle(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)

	numbers := []string{"5511900000001", "5511900000002", "5511900000003"}
	id, err := d.CreateJob(ctx, numbers)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	pending, err := d.Pending(ctx, id)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("pending = %d, want 3", len(pending))
	}
	// Order matters: the send is sequential and must follow the order the person
	// typed the numbers in.
	for i, r := range pending {
		if r.Number != numbers[i] {
			t.Errorf("position %d = %q, want %q", i, r.Number, numbers[i])
		}
	}

	// Two delivered, one failed.
	for i, r := range pending {
		if err := d.MarkRecipient(ctx, r.ID, StatusSending, ""); err != nil {
			t.Fatalf("mark sending: %v", err)
		}
		status, reason := StatusSent, ""
		if i == 1 {
			status, reason = StatusFailed, "número não está no WhatsApp"
		}
		if err := d.MarkRecipient(ctx, r.ID, status, reason); err != nil {
			t.Fatalf("mark result: %v", err)
		}
	}
	if err := d.FinishJob(ctx, id, JobWithErrors); err != nil {
		t.Fatalf("FinishJob: %v", err)
	}

	if active, err := d.ActiveJob(ctx); err != nil || active != nil {
		t.Fatalf("ActiveJob after finishing = %v, %v; want nil, nil", active, err)
	}

	last, err := d.LastJob(ctx)
	if err != nil {
		t.Fatalf("LastJob: %v", err)
	}
	if last.Sent != 2 || last.Failed != 1 {
		t.Errorf("counts = %d sent / %d failed, want 2/1", last.Sent, last.Failed)
	}

	failures, err := d.Failures(ctx, id)
	if err != nil {
		t.Fatalf("Failures: %v", err)
	}
	if len(failures) != 1 || failures[0].Number != numbers[1] {
		t.Fatalf("failures = %+v, want only %q", failures, numbers[1])
	}
	if failures[0].Reason == "" {
		t.Error("the failure has no reason — /falhas would have nothing to explain")
	}
}

func TestOnlyOneActiveJob(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)

	if _, err := d.CreateJob(ctx, []string{"5511900000001"}); err != nil {
		t.Fatal(err)
	}
	active, err := d.ActiveJob(ctx)
	if err != nil || active == nil {
		t.Fatalf("ActiveJob = %v, %v; want a job", active, err)
	}
	// The dispatcher is what refuses a second send, by asking this. The store
	// only has to answer honestly.
	if active.Status != JobRunning {
		t.Errorf("status = %q, want %q", active.Status, JobRunning)
	}
}

func TestRecoveryAfterCrash(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dados", "abraham.db")

	// --- session 1: the send starts and the machine dies mid-flight ---
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	numbers := []string{"5511900000001", "5511900000002", "5511900000003", "5511900000004"}
	id, err := d.CreateJob(ctx, numbers)
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := d.Pending(ctx, id)
	for _, r := range pending[:2] {
		_ = d.MarkRecipient(ctx, r.ID, StatusSending, "")
		_ = d.MarkRecipient(ctx, r.ID, StatusSent, "")
	}
	// The third was in flight when the power went out.
	_ = d.MarkRecipient(ctx, pending[2].ID, StatusSending, "")
	_ = d.Close()

	// --- session 2: the program comes back ---
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	summary, err := d2.RecoverInterrupted(ctx)
	if err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	if summary == nil {
		t.Fatal("the interrupted job was not detected")
	}
	if summary.Sent != 2 {
		t.Errorf("sent = %d, want 2", summary.Sent)
	}
	if summary.Uncertain != 1 {
		t.Errorf("uncertain = %d, want 1 (the one in flight)", summary.Uncertain)
	}
	if summary.NotTried != 1 {
		t.Errorf("not tried = %d, want 1", summary.NotTried)
	}

	// The point that matters most: nothing was resumed. No recipient may be left
	// pending, because that would look like a job to continue and would deliver
	// the message twice.
	left, err := d2.Pending(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("%d recipients left pending — the send would resume and duplicate", len(left))
	}
	if active, _ := d2.ActiveJob(ctx); active != nil {
		t.Error("the job is still marked running after recovery")
	}

	// The ones that did not arrive show up in /falhas, with a reason.
	failures, _ := d2.Failures(ctx, id)
	if len(failures) != 2 {
		t.Fatalf("failures = %d, want 2 (1 uncertain + 1 not tried)", len(failures))
	}
	for _, f := range failures {
		if f.Reason == "" {
			t.Errorf("%s came back with no reason", f.Number)
		}
	}
}

func TestRecoveryInventsNothingOnACleanDatabase(t *testing.T) {
	d := testDB(t)
	summary, err := d.RecoverInterrupted(context.Background())
	if err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	if summary != nil {
		t.Errorf("summary = %+v on a clean database, want nil", summary)
	}
}

func TestCorruptDatabaseIsPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abraham.db")

	// A file that is not a database. This is the real case of a failing disk or
	// a file truncated by an interrupted copy.
	if err := os.WriteFile(path, []byte("this is not a database"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open should have recovered, but failed: %v", err)
	}
	defer d.Close()

	if d.Salvaged == "" {
		t.Fatal("no backup recorded — the user would not know data was lost")
	}
	if _, err := os.Stat(d.Salvaged); err != nil {
		t.Errorf("backup %q is not on disk: %v", d.Salvaged, err)
	}
	// The old file must survive untouched: it holds the WhatsApp session, which
	// is a credential.
	content, err := os.ReadFile(d.Salvaged)
	if err != nil || string(content) != "this is not a database" {
		t.Errorf("the backup did not preserve the original content: %q, %v", content, err)
	}
	// And the new database has to work.
	if _, err := d.CreateJob(context.Background(), []string{"5511900000001"}); err != nil {
		t.Errorf("the fresh database does not work: %v", err)
	}
}
