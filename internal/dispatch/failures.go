package dispatch

import (
	"context"
	"fmt"
	"strings"

	"github.com/wilamis-brasil/abraham/internal/store"
)

// maxPerChunk splits long lists into several messages. One message with 400
// numbers is unreadable on a phone, and some clients truncate it.
const maxPerChunk = 100

// FailuresText builds the /falhas reply as one or more messages.
//
// Priority: if a send is running, show its failures so far; otherwise the ones
// from the last finished send.
func (d *Dispatcher) FailuresText(ctx context.Context) ([]string, error) {
	job, err := d.db.ActiveJob(ctx)
	if err != nil {
		return nil, err
	}
	if job == nil {
		if job, err = d.db.LastJob(ctx); err != nil {
			return nil, err
		}
	}
	if job == nil {
		return []string{"Nenhum envio foi feito ainda."}, nil
	}

	failures, err := d.db.Failures(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	if len(failures) == 0 {
		return []string{"Nenhuma falha no último envio."}, nil
	}

	header := fmt.Sprintf("%d não receberam:", len(failures))
	if job.Status == store.JobInterrupted {
		uncertain := 0
		for _, f := range failures {
			if f.Status == store.StatusUncertain {
				uncertain++
			}
		}
		header = fmt.Sprintf(
			"O envio anterior foi interrompido.\n%d não receberam.", len(failures))
		if uncertain > 0 {
			header += fmt.Sprintf("\n%d ficaram com resultado incerto.", uncertain)
		}
	}

	var messages []string
	for i := 0; i < len(failures); i += maxPerChunk {
		end := min(i+maxPerChunk, len(failures))
		var sb strings.Builder
		if i == 0 {
			sb.WriteString(header)
			sb.WriteString("\n\n")
		}
		for _, f := range failures[i:end] {
			sb.WriteString(f.Number)
			sb.WriteString("\n")
		}
		messages = append(messages, strings.TrimRight(sb.String(), "\n"))
	}
	return messages, nil
}
