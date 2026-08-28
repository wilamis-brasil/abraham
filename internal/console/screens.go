package console

// ui_painel.go — a paleta, os glifos e o painel redesenhado.
//
// Faz parte da fronteira do ui.go: nenhum outro arquivo conhece cor, ANSI ou
// tamanho de janela. (invariante 11)
//
// O console do Abraham nao rola: e um painel fixo, redesenhado por cima de si
// mesmo. Por que nao um log que rola? Porque o programa fica aberto o dia
// inteiro numa maquina de secretaria, e o que importa e o estado agora — quantas
// foram, quantas faltam, se esta conectado —, nao o historico. Historico e o
// arquivo de log.

import (
	"fmt"
	"github.com/wilamis-brasil/abraham/internal/console/art"
	"github.com/wilamis-brasil/abraham/internal/dispatch"
	"github.com/wilamis-brasil/abraham/internal/store"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sys/windows"
)

// ------------------------------------------------------------------ paleta
//
// Vinda do site da escola, invertida para fundo escuro. O verde so aparece em
// dois lugares: o chapeu do mascote e o estado conectado.

var (
	corAmbar    = lipgloss.Color("#FEBF02")
	corGold     = lipgloss.Color("#E7AD00")
	corCreme    = lipgloss.Color("#FAF9F4")
	corAreia    = lipgloss.Color("#8B8578")
	corApagado  = lipgloss.Color("#5A5648")
	corLinha    = lipgloss.Color("#2E2C25")
	corVerdeOk  = lipgloss.Color("#3FBF6E")
	corVermelho = lipgloss.Color("#E0574F")
	corTrilho   = lipgloss.Color("#323026")
)

var (
	esAmbar    = lipgloss.NewStyle().Foreground(corAmbar)
	esAmbarNeg = lipgloss.NewStyle().Foreground(corAmbar).Bold(true)
	esGold     = lipgloss.NewStyle().Foreground(corGold).Bold(true)
	esCreme    = lipgloss.NewStyle().Foreground(corCreme)
	esCremeNeg = lipgloss.NewStyle().Foreground(corCreme).Bold(true)
	esAreia    = lipgloss.NewStyle().Foreground(corAreia)
	esApagado  = lipgloss.NewStyle().Foreground(corApagado)
	esLinha    = lipgloss.NewStyle().Foreground(corLinha)
	esVerde    = lipgloss.NewStyle().Foreground(corVerdeOk)
	esVermelho = lipgloss.NewStyle().Foreground(corVermelho)
	esTrilho   = lipgloss.NewStyle().Foreground(corTrilho)
)

// ------------------------------------------------------------------ glifos
//
// As fontes de console do Windows tem coberturas diferentes, medido com
// fontTools nos arquivos reais:
//
//   - Consolas (padrao do cmd.exe) NAO tem ✓ U+2713, ✕ U+2715, ▶, ⚠.
//   - Lucida Console tambem nao tem ● ▪ ▸ ╭ ━.
//
// Emoji nunca entra no console: a largura quebra no conhost. Emoji so nas
// respostas do WhatsApp.

type Glyphs struct {
	OK, Falha, Ativo, Aviso string
	Cheio, Vazio            string
	Barra                   string
}

var (
	// glyphsRich: Windows Terminal, com Cascadia Mono.
	glyphsRich = Glyphs{
		OK: "✓", Falha: "✕", Ativo: "▸", Aviso: "⚠",
		Cheio: "█", Vazio: "░", Barra: "─",
	}
	// glyphsStandard: qualquer console com Consolas. E o caso mais comum.
	glyphsStandard = Glyphs{
		OK: "√", Falha: "×", Ativo: "▸", Aviso: "▲",
		Cheio: "█", Vazio: "░", Barra: "─",
	}
	// glyphsMinimal: fonte raster antiga, ou --simples.
	glyphsMinimal = Glyphs{
		OK: "+", Falha: "x", Ativo: ">", Aviso: "!",
		Cheio: "#", Vazio: ".", Barra: "-",
	}
)

// pickGlyphs decide o nivel pelo ambiente.
//
// WT_SESSION so existe dentro do Windows Terminal, que traz o Cascadia Mono.
// Nao consultamos a fonte do console pela API do Windows: seria mais uma chamada
// de plataforma para ganhar pouco.
func pickGlyphs(simples bool) Glyphs {
	if simples {
		return glyphsMinimal
	}
	if os.Getenv("WT_SESSION") != "" {
		return glyphsRich
	}
	return glyphsStandard
}

// ------------------------------------------------------------------ painel

const (
	designWidth  = 100
	designHeight = 48
	margin       = 2
	maxEvents    = 6
)

type tela int

const (
	screenBoot tela = iota
	screenPairing
	screenReady
	screenSending
	screenResult
	screenInterrupted
)

type bootStep struct {
	texto  string
	pronto bool
}

type eventLine struct {
	hora   string
	marca  string
	estilo lipgloss.Style
	texto  string
	nota   string
}

// model e tudo que a tela mostra. Uma unica goroutine mexe nele. (invariante 12)
type model struct {
	tela        tela
	conectado   bool
	numero      string
	desde       time.Time
	passos      []bootStep
	eventos     []eventLine
	envio       *dispatch.Progress
	resultado   *dispatch.Result
	interrupcao *store.Interruption
	inicio      time.Time
	qr          []string
	qrLarg      int
	qrAlt       int
	codigo      string
	recado      string
	notas       []string
	giro        int
}

// frame monta o quadro inteiro. Funcao pura: recebe o model e o instante,
// devolve linhas. E o que permite testar a tela sem terminal e, na fase 3,
// testar animacao por quadro em vez de com relogio de parede.
func (s *Output) frame(m *model, agora time.Time) []string {
	larg, _ := s.size()
	var l []string

	switch m.tela {
	case screenPairing:
		l = s.screenPairing(m)
	case screenSending:
		l = s.screenSending(m, larg)
	case screenResult:
		l = s.screenResult(m, larg)
	case screenInterrupted:
		l = s.screenInterrupted(m, larg)
	case screenReady:
		l = s.screenReady(m, larg, agora)
	default:
		l = s.screenBoot(m, larg, agora)
	}
	return l
}

// hero monta mascote + wordmark, ja com a animacao de abertura aplicada.
//
// O tempo entra por parametro, nunca por relogio interno: e o que deixa o quadro
// testavel e o que garante que uma tela redesenhada duas vezes no mesmo instante
// sai identica.
func (s *Output) hero(m *model, agora time.Time) []string {
	decorrido := agora.Sub(m.inicio)
	animating := s.animating()

	larguraWM := len(art.Wordmark[0])
	paint := WordmarkPainter(larguraWM, decorrido, decorrido, animating)
	wordmark := DrawPixels(art.Wordmark, paint)

	gradeMascote := BlinkingMascot(art.Mascot, decorrido, animating)
	mascote := DrawPixels(gradeMascote, PalettePainter)
	visiveis := MascotLinesVisible(len(mascote), decorrido, animating)

	// O wordmark fica opticamente centrado contra a massa do mascote.
	const colWordmark = 36
	const topo = 3

	linhas := make([]string, len(mascote))
	for i, mLinha := range mascote {
		linha := ""
		// O mascote sobe de baixo para cima: as linhas de cima entram por ultimo.
		if i >= len(mascote)-visiveis {
			linha = indent(margin+1) + mLinha
		}
		if w := i - topo; w >= 0 && w < len(wordmark) {
			linha = padTo(linha, colWordmark) + wordmark[w]
		}
		linhas[i] = linha
	}

	// Terminal nao tem opacidade: o fade e feito escurecendo a cor em tres
	// passos.
	switch TaglineFade(decorrido, animating) {
	case 0:
		linhas = append(linhas, "", "")
	case 1:
		linhas = append(linhas,
			padTo("", colWordmark)+esLinha.Render("detetive de disparos do WhatsApp"), "")
	case 2:
		linhas = append(linhas,
			padTo("", colWordmark)+esApagado.Render("detetive de disparos do WhatsApp"),
			padTo("", colWordmark)+esApagado.Render("v"+s.version))
	default:
		linhas = append(linhas,
			padTo("", colWordmark)+esAreia.Render("detetive de disparos do WhatsApp"),
			padTo("", colWordmark)+esGold.Render("v"+s.version)+
				esAreia.Render("  ·  conta única  ·  roda 100% no seu computador"))
	}
	return linhas
}

// animating diz se ha movimento agora.
//
// Durante um envio a animacao para: a atencao e da bar de progresso, e brilho
// competindo com ela e ruido. (regra 2 do anim.go)
func (s *Output) animating() bool {
	return s.painel && !s.semAnimacao
}

func (s *Output) screenBoot(m *model, larg int, agora time.Time) []string {
	l := append([]string{""}, s.hero(m, agora)...)
	l = append(l, "", s.rule(larg), "")
	for _, p := range m.passos {
		marca := esAmbar.Render(s.glifos.Ativo)
		estilo := esAmbar
		if p.pronto {
			marca = esVerde.Render(s.glifos.OK)
			estilo = esCreme
		}
		l = append(l, indent(margin)+marca+" "+estilo.Render(p.texto))
	}
	return append(l, s.footer(m, larg, "iniciando")...)
}

func (s *Output) screenPairing(m *model) []string {
	l := []string{
		"",
		indent(margin) + esAmbarNeg.Render("CONECTAR O WHATSAPP") +
			esAreia.Render("   só na primeira vez"),
		"",
		indent(margin) + esCreme.Render("1  Pegue o celular e abra o WhatsApp"),
		indent(margin) + esCreme.Render("2  Toque no menu e depois em Aparelhos conectados"),
		indent(margin) + esCreme.Render("3  Toque em Conectar aparelho"),
		indent(margin) + esCreme.Render("4  Aponte a câmera para o código abaixo"),
		"",
	}
	l = append(l, m.qr...)
	l = append(l, "", indent(margin)+esApagado.Render(
		"o código se renova sozinho · você não digita nada aqui"))
	if m.codigo != "" {
		l = append(l, "",
			indent(margin)+esCreme.Render("Confira se o celular mostra este mesmo código:"),
			"",
			indent(margin+2)+esAmbarNeg.Render(spaced(m.codigo)),
		)
	}
	return l
}

func (s *Output) screenReady(m *model, larg int, agora time.Time) []string {
	l := append([]string{""}, s.hero(m, agora)...)
	l = append(l, "", s.rule(larg), "", s.connectionLine(m, larg, agora), "")
	l = append(l,
		indent(margin)+esApagado.Render("COMO USAR"),
		indent(margin)+esCreme.Render("Abra a conversa do WhatsApp com você mesmo."),
		indent(margin)+esAreia.Render("É só nessa conversa que o Abraham escuta."),
		"",
	)
	for _, c := range [][2]string{
		{"/enviar", "dispara para até 500 números"},
		{"/cancelar", "interrompe o envio em andamento"},
		{"/falhas", "lista quem não recebeu"},
	} {
		l = append(l, indent(margin+2)+padTo(esGold.Render(c[0]), 13)+esAreia.Render(c[1]))
	}
	l = append(l, "", indent(margin)+esApagado.Render(
		"no /enviar: os números, uma linha em branco, e a mensagem."))
	return append(l, s.footer(m, larg, "aguardando comando")...)
}

func (s *Output) screenSending(m *model, larg int) []string {
	p := m.envio
	if p == nil {
		return s.screenReady(m, larg, time.Now())
	}
	l := []string{
		"",
		indent(margin) + esAmbar.Render(s.glifos.Ativo) + " " +
			esCremeNeg.Render("Enviando") + "   " +
			esAreia.Render(fmt.Sprintf("%d destinatários", p.Total)),
		"",
		indent(margin) + s.bar(p, larg),
		"",
		indent(margin) + s.counters(p),
		"",
		s.rule(larg),
		"",
	}
	l = append(l, s.eventLines(m)...)
	return append(l, s.footer(m, larg,
		"envie /cancelar no WhatsApp para interromper")...)
}

func (s *Output) screenResult(m *model, larg int) []string {
	r := m.resultado
	if r == nil {
		return s.screenReady(m, larg, time.Now())
	}
	marca, titulo, estilo := s.glifos.OK, "Envio concluído", esVerde
	switch r.Status {
	case store.JobCancelled:
		marca, titulo, estilo = s.glifos.Aviso, "Envio cancelado", esAmbar
	case store.JobInterrupted:
		marca, titulo, estilo = s.glifos.Aviso, "Envio interrompido", esAmbar
	case store.JobWithErrors:
		marca, titulo, estilo = s.glifos.Aviso, "Envio concluído com falhas", esAmbar
	}
	l := []string{
		"",
		indent(margin) + estilo.Render(marca) + " " + esCremeNeg.Render(titulo) + "   " +
			esAreia.Render("duração "+shortDuration(r.Duration)),
		"",
		indent(margin) + esVerde.Render(fmt.Sprintf("%d", r.Sent)) + " " +
			esCreme.Render("enviadas") + "    " +
			esVermelho.Render(fmt.Sprintf("%d", r.Failed)) + " " +
			esCreme.Render("não enviadas"),
		"",
		indent(margin) + esAreia.Render("Envie ") + esGold.Render("/falhas") +
			esAreia.Render(" no WhatsApp para ver os números."),
		indent(margin) + esApagado.Render("Nada é reenviado automaticamente."),
		"",
		s.rule(larg),
		"",
	}
	l = append(l, s.eventLines(m)...)
	return append(l, s.footer(m, larg, "aguardando o próximo comando")...)
}

// screenInterrupted aparece no boot, quando sobrou um envio pela metade.
//
// A informacao que mais importa aqui nao e o numero: e a frase dizendo que nada
// foi reenviado. Sem ela, a primeira reacao de quem le e mandar tudo de novo.
func (s *Output) screenInterrupted(m *model, larg int) []string {
	i := m.interrupcao
	if i == nil {
		return s.screenReady(m, larg, time.Now())
	}
	l := []string{
		"",
		indent(margin) + esAmbar.Render(s.glifos.Aviso) + " " +
			esCremeNeg.Render("O envio previous foi interrompido"),
		indent(margin+2) + esAreia.Render("o programa foi encerrado no meio do disparo"),
		"",
		indent(margin) + esVerde.Render(fmt.Sprintf("%d", i.Sent)) + " " +
			esCreme.Render("já enviadas") + "    " +
			esVermelho.Render(fmt.Sprintf("%d", i.NotTried)) + " " +
			esCreme.Render("não enviadas") + "    " +
			esAmbar.Render(fmt.Sprintf("%d", i.Uncertain)) + " " +
			esCreme.Render("com resultado incerto"),
		"",
		indent(margin) + esAreia.Render("O Abraham não reenvia sozinho, para não duplicar mensagens."),
		indent(margin) + esAreia.Render("Envie ") + esGold.Render("/falhas") +
			esAreia.Render(" no WhatsApp para ver a lista."),
		"",
		s.rule(larg),
		"",
	}
	l = append(l, indent(margin)+esApagado.Render("o bot continua funcionando normalmente"))
	return append(l, s.footer(m, larg, "aguardando comando")...)
}

// ------------------------------------------------------------- componentes

func (s *Output) rule(larg int) string {
	n := larg - margin*2
	if n < 4 {
		return ""
	}
	// Os tres primeiros tracos em ambar: um detalhe que amarra a rule a marca
	// sem custar linha nenhuma.
	return indent(margin) + esAmbar.Render(strings.Repeat(s.glifos.Barra, 3)) +
		esLinha.Render(strings.Repeat(s.glifos.Barra, n-3))
}

func (s *Output) connectionLine(m *model, larg int, agora time.Time) string {
	marca, texto, estilo := s.glifos.Falha, "Desconectado", esVermelho
	if m.conectado {
		marca, texto, estilo = "●", "Conectado", esVerde
	} else if s.animating() {
		// Desconectado pulsa: sinaliza que o bot esta tentando voltar, e nao que
		// desistiu.
		estilo = lipgloss.NewStyle().Foreground(
			lipgloss.Color(StatusPulse(agora.Sub(m.inicio), true)))
	}
	esq := indent(margin) + estilo.Render(marca) + " " + esCremeNeg.Render(texto)
	if m.numero != "" {
		esq += "   " + esAreia.Render("+"+MaskPhone(m.numero))
	}
	dir := ""
	if m.conectado && !m.desde.IsZero() {
		dir = esApagado.Render("ativo há " + shortDuration(agora.Sub(m.desde)))
	}
	return spread(esq, dir, larg-margin)
}

func (s *Output) bar(p *dispatch.Progress, larg int) string {
	bw := larg - margin*2 - 18
	if bw < 10 {
		bw = 10
	}
	frac := 0.0
	if p.Total > 0 {
		frac = float64(p.Done) / float64(p.Total)
	}
	cheio := int(frac*float64(bw) + 0.5)
	return esAmbar.Render(strings.Repeat(s.glifos.Cheio, cheio)) +
		esTrilho.Render(strings.Repeat(s.glifos.Vazio, bw-cheio)) +
		"  " + esCremeNeg.Render(fmt.Sprintf("%3.0f%%", frac*100)) +
		"  " + esAreia.Render(fmt.Sprintf("%d/%d", p.Done, p.Total))
}

func (s *Output) counters(p *dispatch.Progress) string {
	linha := esVerde.Render(fmt.Sprintf("%d", p.Sent)) + esAreia.Render(" enviadas") +
		"    " + esVermelho.Render(fmt.Sprintf("%d", p.Failed)) + esAreia.Render(" falhas")
	if p.Remaining > 0 {
		// A estimativa vem pronta do despachante, que e quem conhece o ritmo.
		// Sem media movel do ritmo real: o numero ficaria pulando a cada envio,
		// e o que a pessoa quer saber e a ordem de grandeza.
		restante := p.Remaining
		linha += "    " + esAreia.Render("restam ~"+shortDuration(restante))
	}
	return linha
}

func (s *Output) eventLines(m *model) []string {
	l := make([]string, 0, maxEvents)
	for _, e := range m.eventos {
		linha := indent(margin) + esApagado.Render(e.hora) + "  " +
			e.estilo.Render(e.marca) + "  " + esCreme.Render(e.texto)
		if e.nota != "" {
			linha = padTo(linha, 40) + esAreia.Render(e.nota)
		}
		l = append(l, linha)
	}
	for len(l) < maxEvents {
		l = append(l, "")
	}
	return l
}

func (s *Output) footer(m *model, larg int, estado string) []string {
	mini := MiniMascot()
	l := []string{"", s.rule(larg)}
	for i, linha := range mini {
		esq := indent(margin) + linha
		if i == len(mini)-2 {
			esq = padTo(esq, 20) + esAreia.Render(s.statusText(m, estado))
		}
		l = append(l, esq)
	}
	if len(l) > 2 {
		l[len(l)-1] = spread(l[len(l)-1],
			esApagado.Render("Ctrl+C encerra  ·  abraham "+s.version), larg-margin)
	}
	return l
}

func (s *Output) statusText(m *model, estado string) string {
	if m.recado != "" {
		return m.recado
	}
	return estado
}

// ------------------------------------------------------------------ ajudas

func indent(n int) string { return strings.Repeat(" ", n) }

// padTo completa com espacos ate a coluna dada, contando a largura
// VISIVEL: as sequencias de cor nao ocupam coluna nenhuma na tela.
func padTo(s string, col int) string {
	if l := lipgloss.Width(s); l < col {
		return s + strings.Repeat(" ", col-l)
	}
	return s
}

func spread(esq, dir string, larg int) string {
	espaco := larg - lipgloss.Width(esq) - lipgloss.Width(dir)
	if espaco < 1 {
		espaco = 1
	}
	return esq + strings.Repeat(" ", espaco) + dir
}

func spaced(s string) string {
	return strings.Join(strings.Split(s, ""), " ")
}

func shortDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dmin", h, m)
	case m > 0:
		return fmt.Sprintf("%dmin %ds", m, sec)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

// completeStep marca como pronto o passo em andamento com aquele texto, ou
// acrescenta um novo ja concluido.
func (m *model) completeStep(texto string) {
	for i := range m.passos {
		if m.passos[i].texto == texto || !m.passos[i].pronto {
			m.passos[i].texto = texto
			m.passos[i].pronto = true
			return
		}
	}
	m.passos = append(m.passos, bootStep{texto: texto, pronto: true})
}

// record guarda o evento num anel de tamanho fixo. Sem crescimento sem
// limite: o programa fica aberto o dia inteiro.
func (m *model) record(e eventLine) {
	m.eventos = append(m.eventos, e)
	if len(m.eventos) > maxEvents {
		m.eventos = m.eventos[len(m.eventos)-maxEvents:]
	}
}

// isConsole diz se da para frame um painel. Output redirecionada para arquivo
// ou pipe recebe texto puro, uma linha por acontecimento.
func isConsole(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	var modo uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &modo) == nil
}

// EnableVT liga o processamento de sequencias ANSI no console classico do
// Windows. Sem isso, o conhost antigo imprime os codigos de cor como texto.
// Devolve a funcao que restaura o modo previous.
func EnableVT() func() {
	h := windows.Handle(os.Stdout.Fd())
	var antes uint32
	if err := windows.GetConsoleMode(h, &antes); err != nil {
		return func() {}
	}
	if err := windows.SetConsoleMode(h, antes|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, antes) }
}

var procSetConsoleTitleW = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleTitleW")

// SetupWindow deixa a janela com cara de aplicativo, nao de sessao de
// terminal: titulo "Abraham" na barra e acentuacao correta na saida. Isto
// existia num ABRAHAM.bat com "chcp 65001" — foi trazido para dentro do
// binario para que clicar direto no abraham.exe baste, sem um .bat no meio
// escondendo o icone do programa atras do icone do cmd.exe.
func SetupWindow() {
	_ = windows.SetConsoleOutputCP(65001)
	titulo, err := windows.UTF16PtrFromString("Abraham")
	if err != nil {
		return
	}
	_, _, _ = procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(titulo)))
}
