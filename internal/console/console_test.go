package console

import (
	"bytes"
	"github.com/wilamis-brasil/abraham/internal/console/art"
	"github.com/wilamis-brasil/abraham/internal/dispatch"
	"github.com/wilamis-brasil/abraham/internal/store"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// cargaWhatsApp imita a string de pareamento do whatsmeow no tamanho REAL.
//
// Medido rodando o bot: o QR de verdade sai com 67 modulos. Um payload menor
// dava 49 e me fez subdimensionar a janela na spec — por isso este teste usa uma
// carga do tamanho de cima.
const cargaWhatsApp = "2@k3Jd8sLm+9QwErTyUiOpAsDfGhJkLzXcVbNm1234567890abcdefGHIJKLMNOP" +
	"qrstuvwxyz==,AbCdEfGhIjKlMnOpQrStUvWxYz0123456789+/AbCdEfGhIjKlMnOp==," +
	"9xQz1234567890abcdefghijKLMNOPQRSTUVWXYZ+/0123456789abcdefghij==," +
	"QRSTUVWXYZabcdefghij0123456789+/QRSTUVWXYZabcdefghij0123456789=="

func TestMatrizQRTemZonaDeSilencio(t *testing.T) {
	m, err := QRMatrix(cargaWhatsApp)
	if err != nil {
		t.Fatalf("QRMatrix: %v", err)
	}
	lado := len(m)
	for y := range m {
		if len(m[y]) != lado {
			t.Fatalf("linha %d tem %d colunas, a matriz deveria ser quadrada", y, len(m[y]))
		}
	}
	// A zona de silencio e o que faz a camera achar o codigo. Sem ela, o QR
	// encosta na borda e muitos leitores desistem.
	for y := 0; y < lado; y++ {
		for x := 0; x < lado; x++ {
			naBorda := x < qrQuietZone || y < qrQuietZone || x >= lado-qrQuietZone || y >= lado-qrQuietZone
			if naBorda && m[y][x] {
				t.Fatalf("modulo escuro na zona de silencio em (%d,%d)", x, y)
			}
		}
	}
	// Um QR de verdade tem os tres olhos nos cantos: o pixel logo dentro da
	// borda tem que ser escuro. Se a matriz saisse vazia, o teste acima passaria.
	if !m[qrQuietZone][qrQuietZone] {
		t.Error("o canto superior esquerdo do QR nao esta escuro — a matriz saiu vazia")
	}
}

func TestQRCabeNaJanelaDeProjeto(t *testing.T) {
	linhas, larg, alt, err := DrawQR(cargaWhatsApp)
	if err != nil {
		t.Fatalf("DrawQR: %v", err)
	}
	if len(linhas) != alt {
		t.Fatalf("alt = %d mas vieram %d linhas", alt, len(linhas))
	}
	// Meio-bloco: duas linhas de modulo por linha de terminal.
	if alt != (larg+1)/2 {
		t.Errorf("alt = %d, queria %d (metade de %d)", alt, (larg+1)/2, larg)
	}
	// A janela de projeto e 100x48, e o .bat abre assim. Se o QR passar disso, o
	// usuario ve a tela de "maximize a janela" na primeira execucao — que e
	// exatamente a hora em que ele menos entenderia o que fazer.
	//
	// Os numeros vieram de medir o QR real, nao de estimativa: 67 colunas por 34
	// linhas de terminal.
	const janelaCols, janelaRows = 100, 48
	if larg+2 > janelaCols {
		t.Errorf("o QR precisa de %d colunas, a janela de projeto tem %d", larg+2, janelaCols)
	}
	if alt+qrChrome > janelaRows {
		t.Errorf("o QR precisa de %d linhas, a janela de projeto tem %d", alt+qrChrome, janelaRows)
	}
	t.Logf("QR real: %d colunas x %d linhas de terminal (+%d de moldura)", larg, alt, qrChrome)
	// Cada linha tem que ter exatamente um meio-bloco por coluna de modulo.
	for i, l := range linhas {
		if n := strings.Count(l, "▀"); n != larg {
			t.Fatalf("linha %d tem %d blocos, queria %d", i, n, larg)
		}
	}
}

func TestQRVazioNaoQuebra(t *testing.T) {
	// O whatsmeow pode mandar um evento estranho. Melhor devolver erro do que
	// entrar em panico dentro do laco de eventos.
	if _, _, _, err := DrawQR(""); err != nil {
		t.Logf("string vazia recusada, tudo bem: %v", err)
	}
}

func TestArteTemAsDimensoesDoDesenho(t *testing.T) {
	casos := []struct {
		nome       string
		grade      []string
		querLarg   int
		querLinhas int
	}{
		{"mascote", art.Mascot, 32, 14},
		{"mini", art.Mini, 14, 5},
		{"wordmark", art.Wordmark, 57, 5},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			// A grade tem que ser retangular: uma linha mais curta desloca todo o
			// resto do desenho para a esquerda sem erro nenhum.
			for i, l := range c.grade {
				if len(l) != c.querLarg {
					t.Fatalf("linha %d tem %d colunas, queria %d", i, len(l), c.querLarg)
				}
			}
			// Altura par: meio-bloco processa duas linhas de pixel por vez.
			if len(c.grade)%2 != 0 {
				t.Fatalf("altura impar (%d) — meio-bloco precisa de par", len(c.grade))
			}
			linhas := DrawPixels(c.grade, PalettePainter)
			if len(linhas) != c.querLinhas {
				t.Errorf("saiu com %d linhas de terminal, queria %d", len(linhas), c.querLinhas)
			}
		})
	}
}

func TestArteTransparenteNaoPintaFundo(t *testing.T) {
	// Uma coluna toda transparente tem que sair como espaco, sem sequencia de
	// cor. Se pintassemos o fundo, o desenho ficaria dentro de um retangulo que
	// briga com o tema do terminal.
	grade := []string{"..S..", "..S.."}
	linhas := DrawPixels(grade, PalettePainter)
	if len(linhas) != 1 {
		t.Fatalf("linhas = %d, queria 1", len(linhas))
	}
	if !strings.HasPrefix(linhas[0], "  ") {
		t.Errorf("as colunas transparentes deveriam virar espaco: %q", linhas[0])
	}
}

func TestPintorPersonalizadoRecebeAPosicao(t *testing.T) {
	// E assim que o degrade do wordmark vai colorir por coluna: o renderizador
	// nao sabe o que e um degrade, so pergunta a cor de cada pixel.
	vistas := map[int]bool{}
	DrawPixels([]string{"SSSS", "SSSS"}, func(ch byte, x, y int) string {
		if ch != '.' {
			vistas[x] = true
		}
		return "#FFFFFF"
	})
	if len(vistas) != 4 {
		t.Errorf("o pintor viu %d colunas, queria 4", len(vistas))
	}
}

// saidaDeTeste monta uma Output em modo painel escrevendo num buffer, com o
// tamanho de janela travado para o quadro ser determinístico.
// saidaDeTeste monta um Output sem animação.
//
// Sem isso o quadro depende do relógio: a abertura ainda está correndo, o
// mascote pisca a cada 7 s e o brilho passa a cada 20 s — então dois desenhos
// seguidos diferem por motivos que não têm nada a ver com o que se está
// testando. O teste do diff falhava de vez em quando por causa disso, que é o
// pior tipo de teste: o que passa na sua máquina e quebra no CI.
//
// A animação tem os testes dela em animation_test.go, todos por quadro.
func saidaDeTeste(simples bool) *Output {
	return &Output{painel: true, semAnimacao: true, glifos: pickGlyphs(simples)}
}

func TestQuadroCabeNaJanela(t *testing.T) {
	s := saidaDeTeste(false)
	instante := time.Date(2026, 8, 27, 14, 32, 0, 0, time.UTC)

	casos := []struct {
		nome string
		m    *model
	}{
		{"abertura", &model{tela: screenBoot, passos: []bootStep{
			{"dados locais carregados", true},
			{"sessão do WhatsApp encontrada", true},
			{"conectando ao WhatsApp", false},
		}}},
		{"pronto", &model{tela: screenReady, conectado: true,
			numero: "5511987654321", desde: instante.Add(-3 * time.Minute)}},
		{"enviando", &model{tela: screenSending, conectado: true,
			envio: &dispatch.Progress{Total: 127, Done: 87, Sent: 84, Failed: 3}}},
		{"resultado", &model{tela: screenResult, conectado: true,
			resultado: &dispatch.Result{Status: store.JobWithErrors, Total: 127,
				Sent: 124, Failed: 3, Duration: 4 * time.Minute}}},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			linhas := s.frame(c.m, instante)
			if len(linhas) == 0 {
				t.Fatal("quadro vazio")
			}
			// Nenhuma linha pode passar da largura: o console quebra a linha
			// sozinho e o painel inteiro escorrega para baixo a cada redesenho.
			for i, l := range linhas {
				if w := lipgloss.Width(l); w > designWidth {
					t.Errorf("linha %d tem %d colunas, o limite é %d: %q", i, w, designWidth, l)
				}
			}
			if len(linhas) > designHeight {
				t.Errorf("o quadro tem %d linhas, a janela tem %d", len(linhas), designHeight)
			}
		})
	}
}

func TestQuadroNaoTemNumeroCompleto(t *testing.T) {
	// invariante 13: telefone completo nunca aparece na tela.
	s := saidaDeTeste(false)
	m := &model{tela: screenSending, conectado: true, numero: "5511987654321",
		envio: &dispatch.Progress{Total: 3, Done: 1, Sent: 1}}
	m.record(eventLine{hora: "14:33:02", marca: "+", estilo: esVerde,
		texto: "+" + MaskPhone("5511987654321")})

	quadro := strings.Join(s.frame(m, time.Now()), "\n")
	if strings.Contains(quadro, "5511987654321") {
		t.Error("o telefone completo apareceu no quadro")
	}
	if !strings.Contains(quadro, "5511*****4321") {
		t.Error("o telefone mascarado não apareceu")
	}
}

func TestGlifosPorNivel(t *testing.T) {
	if got := pickGlyphs(true); got.OK != "+" {
		t.Errorf("--simples deveria dar glifos ASCII, veio %q", got.OK)
	}
	t.Setenv("WT_SESSION", "")
	if got := pickGlyphs(false); got.OK != "√" {
		t.Errorf("sem Windows Terminal deveria dar o nível Consolas, veio %q", got.OK)
	}
	t.Setenv("WT_SESSION", "algum-guid")
	if got := pickGlyphs(false); got.OK != "✓" {
		t.Errorf("com Windows Terminal deveria dar o nível rico, veio %q", got.OK)
	}
}

func TestBarraDeProgresso(t *testing.T) {
	s := saidaDeTeste(false)
	for _, c := range []struct{ feitos, total int }{{0, 10}, {5, 10}, {10, 10}, {0, 0}} {
		b := s.bar(&dispatch.Progress{Total: c.total, Done: c.feitos}, designWidth)
		if lipgloss.Width(b) > designWidth-margin*2 {
			t.Errorf("bar %d/%d estourou a largura", c.feitos, c.total)
		}
	}
}

func TestRedesenhoSoReescreveOQueMudou(t *testing.T) {
	// O redesenho ingênuo mandava ~35 KB por quadro (cada pixel do mascote
	// carrega a própria cor), e o console clássico do Windows engasgava com
	// isso 4 vezes por segundo. Este teste é o que impede a regressão.
	var buf bytes.Buffer
	s := &Output{w: &buf, painel: true, glifos: pickGlyphs(false)}
	m := &model{tela: screenReady, conectado: true, numero: "5511987654321",
		desde: time.Now().Add(-time.Minute), inicio: time.Now()}

	s.paint(m)
	primeiro := buf.Len()
	if primeiro == 0 {
		t.Fatal("o primeiro quadro não escreveu nada")
	}

	// Quadro idêntico: não pode tocar no console.
	buf.Reset()
	s.paint(m)
	if n := buf.Len(); n != 0 {
		t.Errorf("quadro sem mudança escreveu %d bytes, queria 0", n)
	}

	// Uma linha diferente: tem que custar uma fração do quadro inteiro.
	buf.Reset()
	m.conectado = false
	s.paint(m)
	parcial := buf.Len()
	if parcial == 0 {
		t.Fatal("a mudança não foi desenhada")
	}
	if parcial > primeiro/4 {
		t.Errorf("mudar uma linha custou %d bytes de um quadro de %d — o diff não está funcionando",
			parcial, primeiro)
	}
}

func TestQuadroNaoEstouraAAlturaDaJanela(t *testing.T) {
	// Quadro mais alto que a janela faz o console rolar, e a cada redesenho o
	// painel escorrega uma linha para cima — que é o "travando" em tela cheia.
	var buf bytes.Buffer
	s := &Output{w: &buf, painel: true, glifos: pickGlyphs(false)}
	m := &model{tela: screenReady, conectado: true, inicio: time.Now()}

	linhas := s.frame(m, time.Now())
	_, alt := s.size()
	if len(linhas) > alt {
		t.Errorf("o quadro tem %d linhas e a janela %d — vai rolar", len(linhas), alt)
	}

	// E o repaint completo não pode terminar com quebra de linha: um \n
	// sobrando no fim empurra a tela para cima a cada quadro.
	s.paint(m)
	saida := buf.String()
	semFim := strings.TrimSuffix(saida, "\x1b[0J")
	if strings.HasSuffix(semFim, "\r\n") {
		t.Error("o quadro termina com quebra de linha — a tela vai rolar a cada redesenho")
	}
}
