package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CreateJob records a new send with every recipient pending.
//
// All in one transaction: either the whole job exists or none of it does. A job
// with half its recipients written would be worse than no job at all.
func (d *DB) CreateJob(ctx context.Context, numbers []string) (int64, error) {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO app_jobs (estado, total, criado_em) VALUES (?, ?, ?)`,
		JobRunning, len(numbers), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO app_recipients (job_id, posicao, numero, estado) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for i, number := range numbers {
		if _, err := stmt.ExecContext(ctx, id, i, number, StatusPending); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// ActiveJob returns the send in progress, or nil if there is none.
func (d *DB) ActiveJob(ctx context.Context) (*Job, error) {
	return d.findJob(ctx, `WHERE estado = ?`, JobRunning)
}

// LastJob returns the most recent send, running or not. This is what /falhas
// consults when nothing is in flight.
func (d *DB) LastJob(ctx context.Context) (*Job, error) {
	return d.findJob(ctx, ``)
}

func (d *DB) findJob(ctx context.Context, filter string, args ...any) (*Job, error) {
	q := `SELECT id, estado, total, criado_em, COALESCE(finalizado_em, 0)
	      FROM app_jobs ` + filter + ` ORDER BY id DESC LIMIT 1`

	var j Job
	var created, finished int64
	err := d.SQL.QueryRowContext(ctx, q, args...).
		Scan(&j.ID, &j.Status, &j.Total, &created, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.CreatedAt = time.Unix(created, 0)
	if finished > 0 {
		j.FinishedAt = time.Unix(finished, 0)
	}
	if err := d.count(ctx, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func (d *DB) count(ctx context.Context, j *Job) error {
	return d.SQL.QueryRowContext(ctx,
		`SELECT
		   COALESCE(SUM(estado = ?), 0),
		   COALESCE(SUM(estado IN (?, ?, ?)), 0)
		 FROM app_recipients WHERE job_id = ?`,
		StatusSent, StatusFailed, StatusUncertain, StatusNotSent, j.ID).
		Scan(&j.Sent, &j.Failed)
}

// Pending returns, in order, whoever has not been attempted yet.
func (d *DB) Pending(ctx context.Context, jobID int64) ([]Recipient, error) {
	return d.list(ctx,
		`WHERE job_id = ? AND estado = ? ORDER BY posicao`, jobID, StatusPending)
}

// Failures returns whoever did not receive the message, including the ambiguous
// results and the ones never attempted because the job was interrupted.
func (d *DB) Failures(ctx context.Context, jobID int64) ([]Recipient, error) {
	return d.list(ctx,
		`WHERE job_id = ? AND estado IN (?, ?, ?) ORDER BY posicao`,
		jobID, StatusFailed, StatusUncertain, StatusNotSent)
}

func (d *DB) list(ctx context.Context, filter string, args ...any) ([]Recipient, error) {
	rows, err := d.SQL.QueryContext(ctx,
		`SELECT id, posicao, numero, estado, motivo FROM app_recipients `+filter, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Recipient
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.ID, &r.Position, &r.Number, &r.Status, &r.Reason); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// MarkRecipient moves one recipient to a new status.
//
// The dispatcher calls this twice per number: pending -> sending before the
// WhatsApp call, and sending -> sent/failed after it. Two steps on purpose: if
// the machine dies in between, the leftover `sending` row is precisely the
// information that this number's outcome is unknown.
func (d *DB) MarkRecipient(ctx context.Context, id int64, status, reason string) error {
	_, err := d.SQL.ExecContext(ctx,
		`UPDATE app_recipients SET estado = ?, motivo = ? WHERE id = ?`, status, reason, id)
	return err
}

// FinishJob closes the send with its final status.
func (d *DB) FinishJob(ctx context.Context, id int64, status string) error {
	_, err := d.SQL.ExecContext(ctx,
		`UPDATE app_jobs SET estado = ?, finalizado_em = ? WHERE id = ?`,
		status, time.Now().Unix(), id)
	return err
}

// Interruption summarises what a job that died mid-flight left behind.
type Interruption struct {
	JobID     int64
	Sent      int
	NotTried  int
	Uncertain int
}

// RecoverInterrupted runs once, at startup.
//
// A job still marked running means the process died mid-send. The job is NOT
// resumed: whoever was in `sending` may or may not have reached the server, and
// re-sending just in case would deliver the message twice to a real person.
// Handing the doubt back through /falhas beats guessing.
func (d *DB) RecoverInterrupted(ctx context.Context) (*Interruption, error) {
	j, err := d.ActiveJob(ctx)
	if err != nil || j == nil {
		return nil, err
	}

	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// The reasons are user-facing: /falhas shows them in Portuguese.
	uncertain, err := move(ctx, tx, j.ID, StatusSending, StatusUncertain,
		"o programa foi encerrado durante o envio")
	if err != nil {
		return nil, err
	}
	notTried, err := move(ctx, tx, j.ID, StatusPending, StatusNotSent,
		"o envio foi interrompido antes de chegar neste número")
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE app_jobs SET estado = ?, finalizado_em = ? WHERE id = ?`,
		JobInterrupted, time.Now().Unix(), j.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &Interruption{
		JobID:     j.ID,
		Sent:      j.Sent,
		NotTried:  notTried,
		Uncertain: uncertain,
	}, nil
}

func move(ctx context.Context, tx *sql.Tx, jobID int64, from, to, reason string) (int, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE app_recipients SET estado = ?, motivo = ? WHERE job_id = ? AND estado = ?`,
		to, reason, jobID, from)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
