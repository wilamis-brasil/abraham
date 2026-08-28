package command

import (
	"strings"
	"unicode"
)

// Accepted length for a normalised phone number. This is syntax sanity only: we
// never infer country or area code, because guessing someone else's phone number
// is the most expensive category of mistake this program could make.
const (
	minDigits = 8
	maxDigits = 15 // E.164 ceiling
)

// entry is one recipient line: a normalised number and an optional name.
type entry struct {
	number string
	name   string
}

// parseNumbers cleans obvious formatting and returns the lines in order,
// **without** de-duplicating. It also returns whatever failed, exactly as the
// person wrote it, so the error message can point at the offending line.
//
// A line may carry a name after the number:
//
//	5511999999999 Maria da Silva
//
// De-duplication happens once, in parseSend, across every block — otherwise a
// repeat inside a block and a repeat between blocks would be counted by
// different code and the reply would under-report.
func parseNumbers(lines []string) (entries []entry, invalid []string) {
	for _, line := range lines {
		raw := strings.TrimSpace(line)
		if raw == "" {
			continue
		}
		numberPart, name := splitNumberAndName(raw)
		number, ok := normalizeNumber(numberPart)
		if !ok {
			invalid = append(invalid, raw)
			continue
		}
		entries = append(entries, entry{number: number, name: name})
	}
	return entries, invalid
}

// splitNumberAndName cuts where the number can no longer continue — not at the
// first space.
//
// "+55 (11) 99999-9999 Maria" has three spaces that belong to the number. The
// rule that works is: the number ends at the first character that could not be
// part of one.
func splitNumberAndName(line string) (number, name string) {
	cut := len(line)
	for i, r := range line {
		if !isNumberChar(r) {
			cut = i
			break
		}
	}
	return strings.TrimSpace(line[:cut]), strings.TrimSpace(line[cut:])
}

func isNumberChar(r rune) bool {
	if unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '+', '(', ')', '-', '.', ' ', '\t', ' ':
		return true
	}
	return false
}

// normalizeNumber strips only the formatting a person writes without thinking:
//
//	+55 (11) 99999-9999  ->  5511999999999
//
// What remains must be digits only. Letters are NOT stripped: if they were,
// "5511abc999999999" would turn into a valid number and the mistake would only
// surface at send time — which is exactly where it must not surface.
func normalizeNumber(raw string) (string, bool) {
	var sb strings.Builder
	sb.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			sb.WriteRune(r)
		case isNumberChar(r):
			// human formatting, discarded
		default:
			return "", false
		}
	}
	number := sb.String()
	if len(number) < minDigits || len(number) > maxDigits {
		return "", false
	}
	if number[0] == '0' {
		return "", false
	}
	return number, true
}
