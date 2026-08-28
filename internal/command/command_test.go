package command

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// textFor is a small helper: the tests care about what each number receives,
// which is the whole point of the package.
func textFor(c Command, number string) (string, bool) {
	for _, t := range c.Targets {
		if t.Number == number {
			return t.Text, true
		}
	}
	return "", false
}

func numbersOf(c Command) []string {
	out := make([]string, 0, len(c.Targets))
	for _, t := range c.Targets {
		out = append(out, t.Number)
	}
	return out
}

// ---------------------------------------------------------------- one block

func TestSingleBlock(t *testing.T) {
	c, err := Parse("/enviar\n5511999999999\n5511888888888\n\nOlá!\n\nAtenciosamente,\nEquipe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Kind != Send {
		t.Fatalf("kind = %v, want Send", c.Kind)
	}
	if len(c.Targets) != 2 {
		t.Fatalf("targets = %#v, want 2", numbersOf(c))
	}
	if c.Blocks != 1 {
		t.Errorf("blocks = %d, want 1", c.Blocks)
	}
	// Everything after the first blank line is the message, including the blank
	// lines that follow it. People write letters in paragraphs.
	text, _ := textFor(c, "5511999999999")
	if !strings.Contains(text, "Atenciosamente") {
		t.Errorf("the message lost content after the second blank line: %q", text)
	}
}

func TestCRLF(t *testing.T) {
	// Windows and some clients send CRLF. If that breaks, the \r becomes part of
	// the number and the whole send is refused without the person understanding
	// why.
	c, err := Parse("/enviar\r\n5511999999999\r\n\r\nOlá!")
	if err != nil {
		t.Fatalf("CRLF should work: %v", err)
	}
	if len(c.Targets) != 1 || c.Targets[0].Number != "5511999999999" {
		t.Fatalf("targets = %#v", c.Targets)
	}
	if c.Targets[0].Text != "Olá!" {
		t.Errorf("text = %q, want %q", c.Targets[0].Text, "Olá!")
	}
}

func TestNumberNormalisation(t *testing.T) {
	cases := []struct {
		raw   string
		want  string
		valid bool
	}{
		{"+55 (11) 99999-9999", "5511999999999", true},
		{"55.11.99999.9999", "5511999999999", true},
		{"  5511999999999  ", "5511999999999", true},
		{"5511abc9999999", "", false},      // a letter must never become a valid number
		{"999", "", false},                 // too short
		{"5511999999999999999", "", false}, // above the E.164 ceiling
		{"05511999999999", "", false},      // leading zero
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeNumber(c.raw)
		if ok != c.valid {
			t.Errorf("normalizeNumber(%q) valid = %v, want %v", c.raw, ok, c.valid)
			continue
		}
		if ok && got != c.want {
			t.Errorf("normalizeNumber(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestDuplicatesKeepTheFirstOccurrence(t *testing.T) {
	c, err := Parse(
		"/enviar\n5511999999999\n+55 (11) 98888-8888\n5511999999999\n5511777777777\n\nOi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"5511999999999", "5511988888888", "5511777777777"}
	got := numbersOf(c)
	if len(got) != len(want) {
		t.Fatalf("numbers = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %q, want %q", i, got[i], want[i])
		}
	}
	if c.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", c.Skipped)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		snippet string
	}{
		{"no blank line", "/enviar\n5511999999999\nOlá!", "linha em branco"},
		{"no numbers", "/enviar\n\nOlá!", "nenhum número"},
		{"empty message", "/enviar\n5511999999999\n\n   \n", "mensagem está vazia"},
		{"invalid number", "/enviar\n5511999999999\nnao-e-numero\n\nOlá!", "inválido"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.input)
			if err == nil {
				t.Fatal("should have been refused")
			}
			var ue *UserError
			if !errors.As(err, &ue) {
				t.Fatalf("error is not a UserError: %T", err)
			}
			if !strings.Contains(err.Error(), c.snippet) {
				t.Errorf("message = %q, want it to contain %q", err.Error(), c.snippet)
			}
			// A single block must never mention block numbers — that would be
			// noise for the overwhelmingly common case.
			if strings.Contains(err.Error(), "ª mensagem") {
				t.Errorf("single block should not mention a block index: %q", err.Error())
			}
		})
	}
}

func TestFiveHundredLimit(t *testing.T) {
	// The limit applies AFTER normalising and de-duplicating: 500 unique numbers
	// pass even if the person pasted 520 lines with repeats.
	var b strings.Builder
	b.WriteString("/enviar\n")
	for i := 0; i < MaxRecipients; i++ {
		fmt.Fprintf(&b, "%d\n", 5511900000000+i)
	}
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "%d\n", 5511900000000+i)
	}
	b.WriteString("\nOlá!")

	c, err := Parse(b.String())
	if err != nil {
		t.Fatalf("500 unique should pass: %v", err)
	}
	if len(c.Targets) != MaxRecipients {
		t.Fatalf("targets = %d, want %d", len(c.Targets), MaxRecipients)
	}

	b.Reset()
	b.WriteString("/enviar\n")
	for i := 0; i <= MaxRecipients; i++ {
		fmt.Fprintf(&b, "%d\n", 5511900000000+i)
	}
	b.WriteString("\nOlá!")
	if _, err := Parse(b.String()); err == nil {
		t.Fatal("501 recipients should be refused")
	} else if !strings.Contains(err.Error(), "limite") {
		t.Errorf("message = %q, want it to mention the limit", err.Error())
	}
}

func TestCancelAndFailures(t *testing.T) {
	for _, c := range []struct {
		input string
		want  Kind
	}{
		{"/cancelar", Cancel},
		{"  /cancelar  ", Cancel},
		{"/CANCELAR", Cancel},
		{"/falhas", Failures},
		{"/falhas\n", Failures},
	} {
		got, err := Parse(c.input)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.input, err)
			continue
		}
		if got.Kind != c.want {
			t.Errorf("Parse(%q).Kind = %v, want %v", c.input, got.Kind, c.want)
		}
	}
}

func TestOrdinaryTextIsIgnored(t *testing.T) {
	// The self-chat is also the person's notepad. Reacting to any text that
	// mentions a command would be a disaster.
	for _, text := range []string{
		"",
		"   ",
		"lembrete: comprar pilha",
		"depois eu mando /cancelar",
		"/cancelar quando der",
		"/falhas de ontem",
		"/enviarr\n5511999999999\n\noi",
		"/menu",
		"/status",
	} {
		c, err := Parse(text)
		if err != nil {
			t.Errorf("Parse(%q) returned %v; ordinary text must be ignored in silence", text, err)
		}
		if c.Kind != None {
			t.Errorf("Parse(%q).Kind = %v, want None", text, c.Kind)
		}
	}
}

// ------------------------------------------------------------------- names

func TestNamePerLine(t *testing.T) {
	c, err := Parse(
		"/enviar\n5511999999999 Maria\n+55 (11) 98888-8888 João da Silva\n5511777777777\n\nOlá {nome}!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The spaces INSIDE a formatted number must not be mistaken for the start of
	// the name: "+55 (11) 98888-8888 João" has three of them before the name.
	want := map[string]string{
		"5511999999999": "Olá Maria!",
		"5511988888888": "Olá João da Silva!",
		"5511777777777": "Olá!",
	}
	for number, wantText := range want {
		got, ok := textFor(c, number)
		if !ok {
			t.Errorf("%s is missing from the targets", number)
			continue
		}
		if got != wantText {
			t.Errorf("%s got %q, want %q", number, got, wantText)
		}
	}
}

func TestNameDoesNotAffectDeduplication(t *testing.T) {
	c, err := Parse("/enviar\n5511999999999 Maria\n5511999999999 Maria Silva\n\nOi {nome}")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Targets) != 1 {
		t.Fatalf("targets = %#v, want one", numbersOf(c))
	}
	if c.Targets[0].Text != "Oi Maria" {
		t.Errorf("the first name should win, got %q", c.Targets[0].Text)
	}
}

func TestInvalidLineWithNameIsStillInvalid(t *testing.T) {
	if _, err := Parse("/enviar\nnao-e-numero Maria\n\nOi"); err == nil {
		t.Error("a line without a valid number must be refused even with a name")
	}
}

func TestPlaceholderWithoutName(t *testing.T) {
	// The placeholder disappears together with the space before it. "Olá , a
	// reunião" is what gives away a machine-written message.
	c, _ := Parse("/enviar\n5511999999999\n\nOlá {nome}, a reunião foi adiada.")
	if got := c.Targets[0].Text; got != "Olá, a reunião foi adiada." {
		t.Errorf("= %q, want %q", got, "Olá, a reunião foi adiada.")
	}
}

func TestPlaceholderAtTheStart(t *testing.T) {
	// With no space in front to swallow, the placeholder simply vanishes.
	c, _ := Parse("/enviar\n5511999999999\n\n{nome} sua matrícula foi confirmada.")
	if got := c.Targets[0].Text; got != " sua matrícula foi confirmada." {
		t.Errorf("= %q", got)
	}
}

func TestMessageWithoutPlaceholderIsUntouched(t *testing.T) {
	c, _ := Parse("/enviar\n5511999999999 Ana\n5511888888888\n\nAviso geral para todos.")
	for _, target := range c.Targets {
		if target.Text != "Aviso geral para todos." {
			t.Errorf("%s got %q", target.Number, target.Text)
		}
	}
}

// ---------------------------------------------------------- several blocks

func TestSeveralMessagesInOneCommand(t *testing.T) {
	c, err := Parse(
		"/enviar\n" +
			"5511900000001 Maria\n" +
			"5511900000002\n" +
			"\n" +
			"Reunião de pais na sexta às 19h.\n" +
			"\n" +
			"/enviar\n" +
			"5511900000003 João\n" +
			"\n" +
			"Olá {nome}, o boletim já está no portal.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Blocks != 2 {
		t.Errorf("blocks = %d, want 2", c.Blocks)
	}
	if len(c.Targets) != 3 {
		t.Fatalf("targets = %#v, want 3", numbersOf(c))
	}

	want := map[string]string{
		"5511900000001": "Reunião de pais na sexta às 19h.",
		"5511900000002": "Reunião de pais na sexta às 19h.",
		"5511900000003": "Olá João, o boletim já está no portal.",
	}
	for number, wantText := range want {
		got, ok := textFor(c, number)
		if !ok {
			t.Errorf("%s is missing", number)
			continue
		}
		if got != wantText {
			t.Errorf("%s got %q, want %q", number, got, wantText)
		}
	}

	// Order is preserved across blocks: the send is sequential and must follow
	// what the person wrote.
	if got := numbersOf(c); got[0] != "5511900000001" || got[2] != "5511900000003" {
		t.Errorf("order = %#v", got)
	}
}

func TestDuplicateAcrossBlocksKeepsTheFirstMessage(t *testing.T) {
	// The same person appearing in two blocks is almost always a mistake. The
	// first block wins and the reply says how many were dropped, so the person
	// can check.
	c, err := Parse(
		"/enviar\n5511900000001\n\nPrimeira mensagem.\n\n" +
			"/enviar\n5511900000001\n5511900000002\n\nSegunda mensagem.")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Targets) != 2 {
		t.Fatalf("targets = %#v, want 2", numbersOf(c))
	}
	if c.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", c.Skipped)
	}
	got, _ := textFor(c, "5511900000001")
	if got != "Primeira mensagem." {
		t.Errorf("the repeated number got %q, want the first block's message", got)
	}
}

func TestErrorInTheSecondBlockSaysWhich(t *testing.T) {
	// With several blocks, "faltou uma linha em branco" alone would send the
	// person hunting through the whole command.
	_, err := Parse(
		"/enviar\n5511900000001\n\nTudo certo aqui.\n\n" +
			"/enviar\n5511900000002\nSem linha em branco.")
	if err == nil {
		t.Fatal("should have been refused")
	}
	if !strings.Contains(err.Error(), "2ª mensagem") {
		t.Errorf("message = %q, want it to point at the second block", err.Error())
	}
}

func TestLimitIsTheTotalAcrossBlocks(t *testing.T) {
	var b strings.Builder
	half := MaxRecipients/2 + 1
	for block := 0; block < 2; block++ {
		b.WriteString("/enviar\n")
		for i := 0; i < half; i++ {
			fmt.Fprintf(&b, "%d\n", 5511900000000+block*1000+i)
		}
		b.WriteString("\nMensagem.\n\n")
	}
	if _, err := Parse(b.String()); err == nil {
		t.Fatal("two blocks over the total limit should be refused")
	} else if !strings.Contains(err.Error(), "limite") {
		t.Errorf("message = %q, want it to mention the limit", err.Error())
	}
}

func TestBlankSectionsAreDropped(t *testing.T) {
	// A stray "/enviar" at the end is a typo, not a block. Refusing the whole
	// command over it would be pedantic, and the error would point at something
	// the person cannot see on screen.
	c, err := Parse("/enviar\n5511900000001\n\nMensagem.\n\n/enviar\n")
	if err != nil {
		t.Fatalf("a trailing /enviar should be ignored, got: %v", err)
	}
	if c.Blocks != 1 {
		t.Errorf("blocks = %d, want 1", c.Blocks)
	}
	if len(c.Targets) != 1 {
		t.Fatalf("targets = %#v, want 1", numbersOf(c))
	}

	// Same for a blank section in the middle.
	c, err = Parse("/enviar\n5511900000001\n\nUma.\n\n/enviar\n\n/enviar\n5511900000002\n\nDuas.")
	if err != nil {
		t.Fatalf("a blank section in the middle should be ignored, got: %v", err)
	}
	if c.Blocks != 2 || len(c.Targets) != 2 {
		t.Errorf("blocks = %d, targets = %#v; want 2 and 2", c.Blocks, numbersOf(c))
	}
}
