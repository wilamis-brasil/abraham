// Package store keeps the little bit of state that must survive a restart:
// the WhatsApp session (owned by whatsmeow) and the last send job.
//
// Two tables of ours live in the same SQLite file as whatsmeow's. We only need
// to know which job ran last and what happened to each number.
//
// What deliberately does NOT live here:
//
//   - The message text. /falhas needs numbers and statuses, never content.
//     Storing the text would create a file with the history of everything the
//     school ever sent, for no use at all. (invariant 9)
//   - Browsable history. There is the current job and the last finished one.
//     That is all.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// dsn — foreign keys MUST come from the DSN.
//
// whatsmeow's Container.Upgrade() reads `PRAGMA foreign_keys` on an arbitrary
// pooled connection and aborts if it comes back off. A one-off PRAGMA after
// Open would only stick on a single connection. The `_pragma=` syntax belongs
// to modernc; the mattn driver would spell it `?_foreign_keys=on`. Proven by
// the smoke test in internal/whatsapp.
const dsn = "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"

// driverName is what modernc registers. Dialect is what whatsmeow understands.
// They happen to match today, but they are different concepts and can drift.
const (
	driverName = "sqlite"

	// Dialect is exported because the WhatsApp adapter hands it to
	// sqlstore.NewWithDB, and it has to agree with the driver above.
	Dialect = "sqlite"
)

// Recipient statuses.
//
// The first four are the normal cycle. The last two only appear after a crash:
// whoever was in `sending` at that moment got an ambiguous result, and whoever
// was still `pending` was simply never attempted.
const (
	StatusPending   = "pending"
	StatusSending   = "sending"
	StatusSent      = "sent"
	StatusFailed    = "failed"
	StatusUncertain = "uncertain"
	StatusNotSent   = "not_sent"
)

// Job statuses.
const (
	JobRunning     = "running"
	JobCompleted   = "completed"
	JobWithErrors  = "with_errors"
	JobCancelled   = "cancelled"
	JobInterrupted = "interrupted"
)

const schema = `
CREATE TABLE IF NOT EXISTS app_jobs (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	estado         TEXT    NOT NULL,
	total          INTEGER NOT NULL,
	criado_em      INTEGER NOT NULL,
	finalizado_em  INTEGER
);

CREATE TABLE IF NOT EXISTS app_recipients (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id   INTEGER NOT NULL REFERENCES app_jobs(id) ON DELETE CASCADE,
	posicao  INTEGER NOT NULL,
	numero   TEXT    NOT NULL,
	estado   TEXT    NOT NULL,
	motivo   TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_recipients_job ON app_recipients(job_id, posicao);
`

// DB is the single connection to the local file.
type DB struct {
	// SQL is shared with the WhatsApp adapter, so whatsmeow's tables and ours
	// live in one file.
	SQL  *sql.DB
	Path string

	// Salvaged holds the backup name when the previous database was corrupt and
	// had to be preserved. Empty in the normal case.
	Salvaged string
}

// Recipient is one row of app_recipients.
type Recipient struct {
	ID       int64
	Position int
	Number   string
	Status   string
	Reason   string // shown to the user by /falhas, so it stays in Portuguese
}

// Job is one row of app_jobs, with the counts already resolved.
type Job struct {
	ID         int64
	Status     string
	Total      int
	CreatedAt  time.Time
	FinishedAt time.Time
	Sent       int
	Failed     int
}

// Open prepares the local file, creating the folder and schema if needed.
//
// If the database exists but is corrupt, it is preserved with a
// `.broken-YYYY-MM-DD-hhmm` suffix and a fresh one takes its place. We never
// delete data silently: the old file holds the WhatsApp session, which is a
// credential, and destroying it without warning would be worse than the defect.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("could not create the data folder: %w", err)
	}

	db, err := connect(path)
	if err == nil {
		if err = createSchema(db); err == nil {
			return &DB{SQL: db, Path: path}, nil
		}
	}
	if db != nil {
		_ = db.Close()
	}
	if !isCorrupt(err) {
		return nil, err
	}

	backup, moveErr := salvageCorrupt(path)
	if moveErr != nil {
		return nil, fmt.Errorf("corrupt database (%v) and the backup failed: %w", err, moveErr)
	}
	db, err = connect(path)
	if err != nil {
		return nil, err
	}
	if err := createSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DB{SQL: db, Path: path, Salvaged: backup}, nil
}

func connect(path string) (*sql.DB, error) {
	db, err := sql.Open(driverName, "file:"+filepath.ToSlash(path)+dsn)
	if err != nil {
		return nil, fmt.Errorf("could not open the local data: %w", err)
	}
	// Open is lazy: this is the first real connection, and the first chance for
	// corruption to show itself.
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return db, fmt.Errorf("integrity check failed: %w", err)
	}
	if result != "ok" {
		return db, fmt.Errorf("local data is corrupt: %s", result)
	}
	return db, nil
}

func createSchema(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("could not prepare the tables: %w", err)
	}
	return nil
}

// isCorrupt separates "the file is damaged" from "the disk is full" or "no
// permission". Only the first case earns the right to recreate the database.
func isCorrupt(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, mark := range []string{
		"is corrupt", // our own message
		"malformed",
		"not a database",
		"database disk image",
		"file is encrypted",
	} {
		if strings.Contains(msg, mark) {
			return true
		}
	}
	return false
}

func salvageCorrupt(path string) (string, error) {
	backup := path + ".broken-" + time.Now().Format("2006-01-02-1504")
	if err := os.Rename(path, backup); err != nil {
		return "", err
	}
	// WAL and shm are useless without the main file.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	return backup, nil
}

// Close ends the connection.
func (d *DB) Close() error {
	if d == nil || d.SQL == nil {
		return nil
	}
	return d.SQL.Close()
}
