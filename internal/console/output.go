package console

// ui.go — a UNICA fronteira com o terminal. (invariante 11)
//
// Nenhum outro arquivo do projeto escreve na tela. Toda saida passa por aqui, e
// um unico goroutine escreve em stdout. (invariante 12)
//
// Nesta fase a saida ainda e texto simples, uma linha por acontecimento. O
// painel desenhado, a paleta e a animacao entram na fase 2 SEM que nenhum outro
// arquivo precise mudar — e exatamente por isso que a fronteira existe.

import (
	"fmt"
	"github.com/wilamis-brasil/abraham/internal/console/art"
	"github.com/wilamis-brasil/abraham/internal/dispatch"
	"github.com/wilamis-brasil/abraham/internal/store"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sys/windows"
	"rsc.io/qr"
)

// Output recebe tudo que o usuario ve.
//
// Uma unica goroutine e dona do model e escreve em stdout. (invariante 12)
// Todo o resto do programa so manda mutacoes por um canal.
type Output struct {
	w           io.Writer
	painel      bool // false quando a saida nao e um console (redirecionada)
	semAnimacao bool
	glifos      Glyphs
	version     string
	mut         chan func(*model)
	pronto      chan struct{}
	fechar      sync.Once

	// previous guarda o quadro que ja esta na tela, para so reescrever as
	// linhas que mudaram. Ver paint().
	previous      []string
	previousWidth int
}

// Options sao as escolhas de aparencia que vem da linha de comando.
type Options struct {
	Simples     bool   // glifos minimos, sem painel
	SemAnimacao bool   // mantem cor, desliga movimento
	Version     string // aparece no heroi e no rodape
}

// New liga a goroutine que desenha.
func New(w io.Writer, op Options) *Output {
	s := &Output{
		w:           w,
		painel:      isConsole(w) && !op.Simples,
		semAnimacao: op.SemAnimacao,
		glifos:      pickGlyphs(op.Simples),
		version:     op.Version,
		mut:         make(chan func(*model), 64),
		pronto:      make(chan struct{}),
	}
	if s.painel {
		// O cursor piscando no meio do desenho parece defeito, e ele ainda
		// salta de posicao a cada redesenho.
		fmt.Fprint(w, "\x1b[?25l")
	}
	go s.laco()
	return s
}

// NewStdout escreve no console.
func NewStdout(op Options) *Output { return New(os.Stdout, op) }

func (s *Output) laco() {
	defer close(s.pronto)
	m := &model{inicio: time.Now()}

	// 4 Hz em repouso: o suficiente para o relogio andar e o ponto de estado
	// pulsar, e barato demais para importar.
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case f, aberto := <-s.mut:
			if !aberto {
				s.paint(m)
				if s.painel {
					// devolve o cursor e desce para o prompt nao escrever por
					// cima do ultimo quadro
					fmt.Fprint(s.w, "\x1b[?25h\r\n")
				}
				return
			}
			f(m)
			s.paint(m)
		case <-tick.C:
			// Redesenha em repouso para o relogio andar e o mascote piscar.
			// Durante um envio quem manda redesenhar e o proprio progresso, e
			// nao o relogio: assim a bar nunca treme entre dois destinatarios.
			if s.painel && m.tela != screenSending {
				m.giro++
				s.paint(m)
			}
		}
	}
}

// paint desenha o quadro por cima do previous, reescrevendo SO as linhas que
// mudaram.
//
// O redesenho ingenuo — reescrever tudo a cada 250 ms — enviava uns 35 KB de
// sequencias ANSI por quadro, porque cada pixel do mascote e do wordmark carrega
// a sua propria cor. O console classico do Windows interpreta isso devagar, e em
// tela cheia (buffer maior) o resultado era a tela engasgando. Com o diff, um
// quadro parado custa uma linha: a do relogio.
//
// Nao usa o buffer alternativo de proposito: ao sair, o ultimo quadro fica na
// tela. Se algo deu errado, e exatamente isso que a pessoa precisa ver.
func (s *Output) paint(m *model) {
	if !s.painel {
		return // no modo texto, quem escreve e o proprio evento
	}
	larg, alt := s.size()
	linhas := s.frame(m, time.Now())

	// Um quadro mais alto que a janela faz o console rolar, e a cada redesenho
	// o painel escorrega uma linha para cima. Melhor cortar o footer.
	if alt > 0 && len(linhas) > alt {
		linhas = linhas[:alt]
	}

	var sb strings.Builder
	completo := s.previous == nil || len(s.previous) != len(linhas) || s.previousWidth != larg
	if completo {
		// Primeiro quadro, ou a janela mudou de tamanho: repinta tudo. O \r\n
		// vai ENTRE as linhas, nunca depois da ultima — um \n sobrando no fim
		// empurra a tela para cima.
		sb.WriteString("\x1b[H")
		for i, l := range linhas {
			if i > 0 {
				sb.WriteString("\r\n")
			}
			sb.WriteString(l)
			sb.WriteString("\x1b[K")
		}
		sb.WriteString("\x1b[0J")
	} else {
		for i, l := range linhas {
			if l == s.previous[i] {
				continue
			}
			fmt.Fprintf(&sb, "\x1b[%d;1H", i+1)
			sb.WriteString(l)
			sb.WriteString("\x1b[K")
		}
	}

	if sb.Len() == 0 {
		return // nada mudou: nem toca no console
	}
	fmt.Fprint(s.w, sb.String())
	s.previous = linhas
	s.previousWidth = larg
}

// mutate enfileira uma mudanca no model.
func (s *Output) mutate(f func(*model)) {
	// Bloqueia de proposito: perder a mensagem que explica um erro seria pior do
	// que segurar quem a produziu por um instante.
	s.mut <- f
}

// Close waits for the queue to drain. Without it, the last lines are lost on
// shutdown — including the one explaining why the program is stopping.
func (s *Output) Close() {
	s.fechar.Do(func() { close(s.mut) })
	<-s.pronto
}

// linha imprime no modo texto puro (saida redirecionada ou --simples).
func (s *Output) line(texto string) {
	if s.painel {
		return
	}
	fmt.Fprintln(s.w, texto)
}

func (s *Output) size() (int, int) {
	c, r := terminalSize()
	if c <= 0 {
		return designWidth, designHeight
	}
	return c, r
}

// Titulo abre uma secao. No painel, vira o recado da bar de estado.
func (s *Output) Headline(texto string) {
	s.mutate(func(m *model) { m.recado = texto })
	s.line("")
	s.line(strings.ToUpper(texto))
}

// Passo marca um item da lista de verificacao do boot como concluido.
func (s *Output) Step(texto string) {
	s.mutate(func(m *model) { m.completeStep(texto) })
	s.line("  " + s.glifos.OK + " " + texto)
}

// Andamento e algo acontecendo agora.
func (s *Output) Working(texto string) {
	s.mutate(func(m *model) { m.passos = append(m.passos, bootStep{texto: texto}) })
	s.line("  " + s.glifos.Ativo + " " + texto)
}

// Aviso e algo que merece atencao, mas nao impede o funcionamento.
func (s *Output) Warn(texto string) {
	s.mutate(func(m *model) { m.recado = texto })
	s.line("  " + s.glifos.Aviso + " " + texto)
}

// Erro e algo que impediu uma acao. Texto curto e em portugues: o detalhe
// tecnico vai para o log, nunca para a tela. (invariante 13)
func (s *Output) Fail(texto string) {
	s.mutate(func(m *model) { m.recado = texto })
	s.line("  " + s.glifos.Falha + " " + texto)
}

// Nota e informacao secundaria.
func (s *Output) Note(texto string) {
	s.mutate(func(m *model) { m.notas = append(m.notas, texto) })
	s.line("    " + texto)
}

// Conectado registra que a conta esta no ar.
func (s *Output) Connected(numero string) {
	s.mutate(func(m *model) {
		m.conectado = true
		m.numero = numero
		if m.desde.IsZero() {
			m.desde = time.Now()
		}
		if m.tela == screenBoot || m.tela == screenPairing {
			m.tela = screenReady
		}
		m.qr = nil
		m.codigo = ""
	})
}

// Desconectado registra a queda.
func (s *Output) Disconnected() {
	s.mutate(func(m *model) { m.conectado = false })
}

// Codigo mostra o codigo curto de conferencia do pareamento.
func (s *Output) PairingCode(codigo string) {
	s.mutate(func(m *model) { m.codigo = codigo })
	s.line("")
	s.line("      " + spaced(codigo))
	s.line("")
}

// Interrompido mostra o que sobrou de um envio que morreu no meio.
func (s *Output) Interrupted(i store.Interruption) {
	s.mutate(func(m *model) {
		m.tela = screenInterrupted
		m.interrupcao = &i
	})
	s.line("")
	s.line(fmt.Sprintf("  %s O envio previous foi interrompido.", s.glifos.Aviso))
	s.line(fmt.Sprintf("    %d já enviadas · %d não enviadas · %d com resultado incerto",
		i.Sent, i.NotTried, i.Uncertain))
	s.line("    Nada é reenviado automaticamente. Use /falhas no WhatsApp.")
}

// IniciarEnvio troca a tela para o acompanhamento do disparo.
func (s *Output) SendStarted(total int) {
	s.mutate(func(m *model) {
		m.tela = screenSending
		m.envio = &dispatch.Progress{Total: total}
		m.eventos = nil
		m.resultado = nil
	})
}

// Resultado troca a tela para o fecho do envio.
func (s *Output) SendFinished(r dispatch.Result) {
	s.mutate(func(m *model) {
		m.tela = screenResult
		m.resultado = &r
		m.envio = nil
	})
}

// QR desenha o codigo de pareamento na tela.
//
// O codigo em si NUNCA vai para o log nem e impresso como texto: ele e um token
// de pareamento, e quem o tiver pode assumir a conta. (invariante 13) Ele so
// existe como desenho, para a camera do celular ler.
func (s *Output) QR(codigo string) {
	linhas, larg, alt, err := DrawQR(codigo)
	if err != nil {
		s.Fail("não consegui desenhar o código de conexão.")
		return
	}
	s.mutate(func(m *model) {
		m.tela = screenPairing
		m.qr, m.qrLarg, m.qrAlt = linhas, larg, alt
	})

	// cols == 0 significa que nao da para saber o tamanho (saida redirecionada,
	// por exemplo). Nesse caso desenhamos assim mesmo: o guard existe para
	// janela pequena de verdade, nao para incerteza.
	cols, rows := terminalSize()
	if cols > 0 && (cols < larg+2 || rows < alt+qrChrome) {
		// Melhor pedir uma janela maior do que frame um QR picado que a
		// camera nao le e o usuario nao entende por que.
		s.line("")
		s.Warn("a janela está pequena para mostrar o código de conexão.")
		s.Note(fmt.Sprintf("preciso de %d colunas por %d linhas; a janela tem %d por %d.",
			larg+2, alt+qrChrome, cols, rows))
		s.Note("maximize a janela — o código aparece sozinho.")
		return
	}

	if s.painel {
		return // a tela de pareamento ja desenha o QR
	}
	s.line("")
	s.line("  CONECTAR O WHATSAPP  ·  no celular: menu › Aparelhos conectados ›")
	s.line("  Conectar aparelho › aponte a câmera para o código abaixo")
	s.line("")
	for _, l := range linhas {
		s.line(l)
	}
	s.line("    o código se renova sozinho · você não digita nada aqui")
}

// qrChrome e quanto espaco o cabecalho e o footer ocupam em volta do desenho.
//
// Foi apertado depois de medir o QR real: ele sai com 61 modulos, e nao com os
// 49 que um payload sintetico sugeria. Cada linha de texto em volta custa uma
// linha de janela que o usuario precisa ter.
const qrChrome = 6

// Cores do QR. Fixas de proposito: o modulo escuro nao pode ser a cor de fundo
// do terminal, senao em tema claro a camera nao enxerga contraste nenhum.
var (
	qrDark  = lipgloss.Color("#14140F")
	qrLight = lipgloss.Color("#FAF9F4")
)

// qrQuietZone e a zona de silencio em modulos. Sem ela a camera nao acha o codigo.
const qrQuietZone = 2

// DrawQR converte o texto de pareamento em linhas prontas para a tela.
//
// Cada linha de terminal carrega DUAS linhas de modulos, usando meio-bloco: a
// cor de frente pinta o modulo de cima e a de fundo pinta o de baixo. Sem esse
// truque o QR sairia com o dobro da altura e nao caberia em janela nenhuma.
func DrawQR(texto string) (linhas []string, larg, alt int, err error) {
	m, err := QRMatrix(texto)
	if err != nil {
		return nil, 0, 0, err
	}
	lado := len(m)

	// Quatro combinacoes possiveis de (cima, baixo). Pre-montadas porque montar
	// um estilo por celula seria milhares de alocacoes por quadro.
	celula := [2][2]string{}
	for cima := 0; cima < 2; cima++ {
		for baixo := 0; baixo < 2; baixo++ {
			celula[cima][baixo] = lipgloss.NewStyle().
				Foreground(qrColor(cima == 1)).
				Background(qrColor(baixo == 1)).
				Render("▀")
		}
	}

	var sb strings.Builder
	for y := 0; y < lado; y += 2 {
		sb.Reset()
		for x := 0; x < lado; x++ {
			cima := b2i(m[y][x])
			baixo := 0 // fora da matriz e zona de silencio, ou seja, claro
			if y+1 < lado {
				baixo = b2i(m[y+1][x])
			}
			sb.WriteString(celula[cima][baixo])
		}
		linhas = append(linhas, sb.String())
	}
	return linhas, lado, len(linhas), nil
}

func qrColor(escuro bool) lipgloss.Color {
	if escuro {
		return qrDark
	}
	return qrLight
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// QRMatrix devolve os modulos ja com a zona de silencio. true = escuro.
//
// Separada do desenho para o teste poder conferir que o que foi pintado e
// exatamente o que o codificador produziu.
func QRMatrix(texto string) ([][]bool, error) {
	// Nivel L: o codigo aparece numa tela limpa, sem risco de sujeira ou dobra,
	// entao correcao de erro alta so aumentaria o tamanho a toa.
	c, err := qr.Encode(texto, qr.L)
	if err != nil {
		return nil, err
	}
	lado := c.Size + qrQuietZone*2
	m := make([][]bool, lado)
	for y := range m {
		m[y] = make([]bool, lado)
		for x := range m[y] {
			mx, my := x-qrQuietZone, y-qrQuietZone
			if mx >= 0 && my >= 0 && mx < c.Size && my < c.Size {
				m[y][x] = c.Black(mx, my)
			}
		}
	}
	return m, nil
}

// terminalSize devolve a janela visivel, nao o buffer de rolagem.
// Devolve 0,0 quando nao da para saber — saida redirecionada, por exemplo.
func terminalSize() (cols, rows int) {
	var info windows.ConsoleScreenBufferInfo
	h := windows.Handle(os.Stdout.Fd())
	if err := windows.GetConsoleScreenBufferInfo(h, &info); err != nil {
		return 0, 0 // desconhecido; quem chama decide o que fazer
	}
	return int(info.Window.Right - info.Window.Left + 1),
		int(info.Window.Bottom - info.Window.Top + 1)
}

// ------------------------------------------------------------- arte pixel

// halfBlock e o caractere que dobra a resolucao vertical: a cor de frente pinta
// o pixel de cima e a de fundo pinta o de baixo. Sem ele, o mascote sairia com o
// dobro da altura e ficaria esticado, porque uma celula de terminal e cerca de
// duas vezes mais alta do que larga.
const halfBlock = "▀"

// DrawPixels converte uma grade de pixels em linhas de terminal.
//
// A grade vem de art.go, com um caractere por pixel. '.' e transparente: o
// terminal aparece atras, o que evita paint um retangulo de fundo que brigaria
// com o tema do usuario.
//
// paint recebe o caractere da grade e devolve a cor. Passar isso por fora e o
// que permite o degrade animado do wordmark colorir por coluna sem que o
// renderizador saiba o que e um degrade.
func DrawPixels(grade []string, paint func(ch byte, x, y int) string) []string {
	if len(grade) == 0 {
		return nil
	}
	larg := len(grade[0])
	linhas := make([]string, 0, (len(grade)+1)/2)

	var sb strings.Builder
	for y := 0; y < len(grade); y += 2 {
		sb.Reset()
		for x := 0; x < larg; x++ {
			cima := pixel(grade, x, y)
			baixo := pixel(grade, x, y+1)
			sb.WriteString(pixelCell(paint, cima, baixo, x, y))
		}
		linhas = append(linhas, strings.TrimRight(sb.String(), " "))
	}
	return linhas
}

func pixel(grade []string, x, y int) byte {
	if y >= len(grade) || x >= len(grade[y]) {
		return '.'
	}
	return grade[y][x]
}

func pixelCell(paint func(byte, int, int) string, cima, baixo byte, x, y int) string {
	corCima := paint(cima, x, y)
	corBaixo := paint(baixo, x, y+1)

	switch {
	case corCima == "" && corBaixo == "":
		return " " // os dois transparentes: deixa o terminal aparecer
	case corBaixo == "":
		// so o de cima: meio-bloco pintado, fundo do terminal embaixo
		return lipgloss.NewStyle().Foreground(lipgloss.Color(corCima)).Render(halfBlock)
	case corCima == "":
		// so o de baixo: usa o meio-bloco INFERIOR, senao precisariamos paint o
		// fundo e o transparente deixaria de ser transparente
		return lipgloss.NewStyle().Foreground(lipgloss.Color(corBaixo)).Render("▄")
	default:
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(corCima)).
			Background(lipgloss.Color(corBaixo)).
			Render(halfBlock)
	}
}

// PalettePainter e o pintor padrao: cada caractere vira sua cor fixa de art.go.
func PalettePainter(ch byte, x, y int) string { return art.Palette[ch] }

// MiniMascot desenha a versao pequena, para cabecalho e footer.
func MiniMascot() []string { return DrawPixels(art.Mini, PalettePainter) }

// ------------------------------------------------------------------------

// Progresso registra o resultado de um destinatario.
func (s *Output) Progress(p dispatch.Progress) {
	if p.Status == store.StatusSending {
		return // o "enviando" ja aparece na bar; como linha so poluiria
	}
	marca, estilo := s.glifos.OK, esVerde
	if p.Status != store.StatusSent {
		marca, estilo = s.glifos.Falha, esVermelho
	}
	s.mutate(func(m *model) {
		m.envio = &p
		m.record(eventLine{
			hora:   time.Now().Format("15:04:05"),
			marca:  marca,
			estilo: estilo,
			texto:  "+" + MaskPhone(p.Number),
			nota:   p.Reason,
		})
	})
	texto := fmt.Sprintf("  %s %s  %d/%d", marca, MaskPhone(p.Number), p.Done, p.Total)
	if p.Reason != "" {
		texto += "  " + p.Reason
	}
	s.line(texto)
}

// MaskPhone esconde o miolo do telefone.
//
// Vale para a tela e para o log. A lista completa existe no db, porque o
// /falhas precisa devolve-la pelo WhatsApp; em qualquer outro lugar, numero
// completo e vazamento. (invariante 13)
func MaskPhone(numero string) string {
	if len(numero) < 8 {
		return "****"
	}
	return numero[:4] + strings.Repeat("*", len(numero)-8) + numero[len(numero)-4:]
}

// ---------------------------------------------------------------- ajuda
//
// Estes dois escrevem direto, sem passar pelo painel, porque acontecem antes de
// existir uma tela e o programa sai logo depois. Ficam aqui, e nao no main.go,
// para o invariante 11 nao precisar de excecao: se ele tiver excecao, ninguem
// consegue confiar numa busca por fmt.Print para achar violacao.

// PrintVersion escreve a versao e nada mais. Feito para script: `abraham
// --versao` deve dar uma linha que da para comparar.
func PrintVersion(version string) {
	fmt.Println("abraham " + version)
}

// PrintHelp explica o programa para quem nunca abriu um terminal.
func PrintHelp(version string) {
	fmt.Print("Abraham " + version + ` — detetive de disparos do WhatsApp

O QUE É
  Um programa local que dispara mensagens de WhatsApp para uma lista de
  números. Roda no seu computador; nada vai para a internet além do próprio
  WhatsApp.

COMO USAR
  Abra o Abraham e, na primeira vez, conecte o celular pelo código na tela.
  Depois é só usar o WhatsApp normalmente. Os comandos vão na conversa
  consigo mesmo — e só nela.

    /enviar      dispara para até 500 números
    /cancelar    interrompe o envio em andamento
    /falhas      lista quem não recebeu

  O /enviar leva os números, uma linha em branco, e a mensagem:

    /enviar
    5511999999999 Maria
    5511888888888

    Olá {nome}, esta é a mensagem.

  O nome depois do número é opcional. Quando existe, o {nome} da mensagem
  vira ele; quando não existe, o {nome} some e a frase sai limpa.

  Entre uma mensagem e a próxima o Abraham espera de 1 a 7 segundos, então
  500 destinatários levam uns 33 minutos. Deixe a janela aberta.

OPÇÕES
  --simples        para consoles antigos: sem painel, sem cor, glifos ASCII
  --sem-animacao   mantém as cores, desliga o movimento
  --versao         mostra a versão
  --ajuda          mostra isto

  ABRAHAM_PROXY    variável de ambiente, só se a rede bloquear o WhatsApp
                   (http://, https:// ou socks5://). Não protege a conta
                   de restrição — ban mira o número, não o IP.

ONDE FICAM AS COISAS
  dados/   a sessão do WhatsApp e o último envio. Trate como senha:
           não mande por e-mail nem coloque em repositório.
  logs/    um arquivo por dia, apagados depois de 30 dias.
           Telefones aparecem mascarados.
`)
}
