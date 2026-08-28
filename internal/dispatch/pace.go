package dispatch

import (
	"context"
	"math/rand"
	"strings"
	"time"
)

const (
	// The wait between recipients is drawn from this range.
	//
	// What actually reduces load is the AVERAGE, 4s here — double the fixed 2s
	// this used to have. The randomisation helps at the margin: it breaks the
	// exact cadence, which is the most obvious signature of an automated send.
	//
	// This is still NOT an "anti-ban" measure. There is no documented rate that
	// turns unofficial automation into authorised use, and what actually gets an
	// account restricted is blocks and reports from the people receiving, not
	// speed.
	defaultMinInterval = 1 * time.Second
	defaultMaxInterval = 7 * time.Second
)

// sleep waits a drawn amount of time. Returns false if the job was cancelled
// during the wait — so a /cancelar does not have to sit through up to 7 seconds
// before taking effect.
func (d *Dispatcher) sleep(ctx context.Context) bool {
	wait := d.drawInterval()
	if wait <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

// drawInterval picks a wait between the minimum and maximum, inclusive.
func (d *Dispatcher) drawInterval() time.Duration {
	lo, hi := d.MinInterval, d.MaxInterval
	if hi < lo {
		lo, hi = hi, lo
	}
	if hi <= 0 {
		return 0
	}
	if hi == lo {
		return lo
	}
	return lo + time.Duration(rand.Int63n(int64(hi-lo)+1))
}

// EstimatedDuration says how long a send of n recipients should take.
//
// It exists to warn BEFORE starting: at 1 to 7 seconds apiece, 500 messages run
// past half an hour, and someone who does not know that closes the window
// half-way through thinking it froze.
func (d *Dispatcher) EstimatedDuration(n int) time.Duration {
	if n <= 1 {
		return 0
	}
	average := (d.MinInterval + d.MaxInterval) / 2
	return time.Duration(n-1) * average
}

// describeError turns the technical error into a short phrase for /falhas.
// The full error goes to the log, never to WhatsApp.
func describeError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not on whatsapp") || strings.Contains(msg, "not found"):
		return "número não está no WhatsApp"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "o WhatsApp não respondeu a tempo"
	case strings.Contains(msg, "not connected") || strings.Contains(msg, "websocket"):
		return "a conexão caiu neste momento"
	case strings.Contains(msg, "rate") || strings.Contains(msg, "limit"):
		return "o WhatsApp recusou por excesso de envios"
	default:
		return "não foi possível enviar"
	}
}
