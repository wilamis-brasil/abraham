// Package command turns what the person typed into what the bot will do.
//
// There are exactly three commands: /enviar, /cancelar and /falhas. Any other
// text is ignored in silence, including text that merely looks like a command —
// the self-chat is also where people keep notes.
//
// The /enviar contract is rigid on purpose. Rigidity here removes dozens of
// special cases downstream and, more importantly, lets the whole thing be
// validated BEFORE the first message goes out. Discovering at recipient 270
// that the input was malformed, after 269 real people already got the message,
// is not a recoverable mistake.
//
// The command words stay in Portuguese: they are the product surface, typed
// every day by the people who run it.
package command

import (
	"fmt"
	"strings"
)

// MaxRecipients is a limit of our product, not a "safe WhatsApp number" — that
// number does not exist. It applies to the total across every block, after
// normalising and de-duplicating.
const MaxRecipients = 500

// namePlaceholder is what a message can use to greet each person by name.
const namePlaceholder = "{nome}"

// Kind identifies which of the three arrived.
type Kind int

const (
	None Kind = iota
	Send
	Cancel
	Failures
)

// Target is one recipient together with the exact text they will receive.
//
// The parser flattens every block into a list of these, so nothing downstream
// needs to know that blocks exist.
type Target struct {
	Number string
	Text   string // {nome} already substituted
}

// Command is the result of reading one self-chat message.
type Command struct {
	Kind Kind

	// Targets, Blocks and Skipped only carry meaning for Send.
	//
	// The texts live here in memory and never reach the database or the log.
	// (invariant 9)
	Targets []Target
	Blocks  int // how many /enviar blocks produced these targets
	Skipped int // duplicates dropped, so the reply can mention them
}

// UserError is an error that can be shown to the person exactly as it is.
//
// Every error in this package is of that nature: short, in Portuguese, no
// jargon. Real technical errors never come through here — they go to the log.
type UserError struct{ Text string }

func (e *UserError) Error() string { return e.Text }

func reject(format string, args ...any) error {
	return &UserError{Text: fmt.Sprintf(format, args...)}
}

// Parse reads one message from the self-chat.
//
// Returns Command{Kind: None} with a nil error when the text simply is not a
// command — by far the most common case. Returns a *UserError when it is a
// malformed /enviar: then the person made a mistake and needs to know.
func Parse(text string) (Command, error) {
	lines := splitLines(text)
	if len(lines) == 0 {
		return Command{}, nil
	}

	switch strings.ToLower(strings.TrimSpace(lines[0])) {
	case "/cancelar":
		if hasContentAfterFirstLine(lines) {
			return Command{}, nil
		}
		return Command{Kind: Cancel}, nil

	case "/falhas":
		if hasContentAfterFirstLine(lines) {
			return Command{}, nil
		}
		return Command{Kind: Failures}, nil

	case "/enviar":
		return parseSend(lines[1:])
	}
	return Command{}, nil
}

// splitLines accepts LF and CRLF. Windows, phones and the clipboard all
// represent line breaks differently, and the person has no way of knowing which
// one they are using.
func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// hasContentAfterFirstLine stops a message that merely mentions "/cancelar" in
// the middle of a sentence from cancelling a real send.
func hasContentAfterFirstLine(lines []string) bool {
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}

// parseSend reads one or more blocks.
//
// A second /enviar inside the same message starts a new block, so different
// groups can get different texts without inventing any new syntax:
//
//	/enviar
//	5511999999999 Maria
//
//	Reunião de pais na sexta.
//
//	/enviar
//	5511888888888
//
//	O boletim já está no portal.
//
// One block behaves exactly as it always did — nothing changes for anyone who
// already uses this.
func parseSend(rest []string) (Command, error) {
	blocks := splitBlocks(rest)

	var (
		targets []Target
		seen    = make(map[string]bool)
		skipped int
	)

	for i, block := range blocks {
		entries, invalid, err := readBlock(block)
		if err != nil {
			return Command{}, blockError(err, i, len(blocks))
		}
		if len(invalid) > 0 {
			return Command{}, blockError(reject(
				"%s. Verifique %s.",
				plural(len(invalid), "1 número inválido", "%d números inválidos"),
				sample(invalid)), i, len(blocks))
		}

		for _, e := range entries {
			// First occurrence wins, inside a block and across blocks. Sending
			// two messages to the same person in one command is almost always a
			// mistake, and the reply says how many were dropped.
			if seen[e.number] {
				skipped++
				continue
			}
			seen[e.number] = true
			targets = append(targets, Target{
				Number: e.number,
				Text:   applyName(block.message, e.name),
			})
		}
	}

	switch {
	case len(targets) == 0:
		return Command{}, reject("Envio não iniciado: nenhum número foi informado.")
	case len(targets) > MaxRecipients:
		return Command{}, reject(
			"Envio não iniciado: são %d destinatários e o limite é %d.",
			len(targets), MaxRecipients)
	}

	return Command{
		Kind:    Send,
		Targets: targets,
		Blocks:  len(blocks),
		Skipped: skipped,
	}, nil
}

// block is the raw shape of one /enviar section, before validation.
type block struct {
	numberLines []string
	message     string
	separated   bool // whether the blank line separating numbers from text existed
}

// splitBlocks cuts at every line that is exactly "/enviar".
//
// Entirely blank sections are dropped. A stray "/enviar" at the end of the
// message is a typo, not a block: refusing the whole command over it would be
// pedantic, and the error would point at something the person cannot see.
func splitBlocks(lines []string) []block {
	var (
		blocks  []block
		current []string
	)
	flush := func() {
		if b, ok := buildBlock(current); ok {
			blocks = append(blocks, b)
		}
		current = nil
	}
	for _, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), "/enviar") {
			flush()
			continue
		}
		current = append(current, l)
	}
	flush()
	return blocks
}

// buildBlock returns false when the section is entirely blank, so the caller
// can drop it.
func buildBlock(lines []string) (block, bool) {
	blank := true
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			blank = false
			break
		}
	}
	if blank {
		return block{}, false
	}
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			return block{
				numberLines: lines[:i],
				message:     strings.TrimSpace(strings.Join(lines[i+1:], "\n")),
				separated:   true,
			}, true
		}
	}
	return block{numberLines: lines}, true
}

func readBlock(b block) (entries []entry, invalid []string, err error) {
	if !b.separated {
		return nil, nil, reject(
			"faltou uma linha em branco entre os números e a mensagem")
	}
	entries, invalid = parseNumbers(b.numberLines)
	if len(entries) == 0 && len(invalid) == 0 {
		return nil, nil, reject("nenhum número foi informado")
	}
	if b.message == "" {
		return nil, nil, reject("a mensagem está vazia")
	}
	return entries, invalid, nil
}

// blockError puts the problem in context. With a single block the message is
// exactly what it always was; with several, it says which one is broken —
// otherwise the person has to hunt through the whole command.
func blockError(err error, index, total int) error {
	if total == 1 {
		return reject("Envio não iniciado: %s.", strings.TrimSuffix(err.Error(), "."))
	}
	return reject("Envio não iniciado: na %dª mensagem, %s.",
		index+1, strings.TrimSuffix(err.Error(), "."))
}

// applyName swaps the placeholder for this person's name.
//
// With no name the placeholder disappears together with the single space before
// it: "Olá {nome}," becomes "Olá," and not "Olá ,". That stray space is exactly
// the kind of detail that tells the reader a machine wrote the message.
func applyName(message, name string) string {
	if name != "" {
		return strings.ReplaceAll(message, namePlaceholder, name)
	}
	var sb strings.Builder
	sb.Grow(len(message))
	rest := message
	for {
		i := strings.Index(rest, namePlaceholder)
		if i < 0 {
			sb.WriteString(rest)
			break
		}
		before := strings.TrimSuffix(rest[:i], " ")
		sb.WriteString(before)
		rest = rest[i+len(namePlaceholder):]
	}
	return sb.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}

// sample quotes at most three examples. The message arrives over WhatsApp;
// dumping forty wrong numbers into it helps nobody find the problem.
func sample(items []string) string {
	if len(items) <= 3 {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:3], ", ") + fmt.Sprintf(" e mais %d", len(items)-3)
}
