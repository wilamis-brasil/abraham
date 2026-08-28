package console

import (
	"github.com/wilamis-brasil/abraham/internal/console/art"
	"strings"
	"testing"
	"time"
)

func TestRevelacaoDoWordmark(t *testing.T) {
	const larg = 57
	conta := func(d time.Duration) int {
		paint := WordmarkPainter(larg, d, 0, true)
		n := 0
		for x := 0; x < larg; x++ {
			if paint('#', x, 0) != "" {
				n++
			}
		}
		return n
	}

	// No instante zero nao ha nada; no fim da revelacao ha tudo. E o meio tem que
	// estar no meio — se a interpolacao quebrar, isto pega.
	if n := conta(0); n != 0 {
		t.Errorf("no quadro 0 apareceram %d colunas, queria 0", n)
	}
	if n := conta(IntroDuration); n != larg {
		t.Errorf("no fim apareceram %d colunas, queria %d", n, larg)
	}
	meio := conta(wordmarkReveal / 2)
	if meio == 0 || meio == larg {
		t.Errorf("no meio apareceram %d colunas — a revelação não é gradual", meio)
	}

	// easeOutCubic desacelera: na metade do tempo mais da metade ja apareceu.
	if meio <= larg/2 {
		t.Errorf("na metade do tempo apareceram %d de %d — a curva não está desacelerando", meio, larg)
	}
}

func TestRevelacaoEMonotona(t *testing.T) {
	// Nenhuma letra pode desaparecer depois de ter aparecido. Um retrocesso na
	// curva pisca a tela e chama mais atencao que a propria animacao.
	const larg = 57
	previous := -1
	for q := 0; q <= 28; q++ {
		d := time.Duration(q) * IntroDuration / 28
		paint := WordmarkPainter(larg, d, 0, true)
		n := 0
		for x := 0; x < larg; x++ {
			if paint('#', x, 0) != "" {
				n++
			}
		}
		if n < previous {
			t.Fatalf("quadro %d mostrou %d colunas depois de %d — a revelação retrocedeu", q, n, previous)
		}
		previous = n
	}
}

func TestBrilhoSoPassaUmaVezPorCiclo(t *testing.T) {
	const larg = 57
	// Depois da abertura, em repouso, a cor tem que ser ambar chapado na maior
	// parte do ciclo.
	paint := WordmarkPainter(larg, 10*time.Second, 5*time.Second, true)
	if got := paint('#', 10, 0); got != "#FEBF02" {
		t.Errorf("em repouso a cor = %q, queria âmbar chapado", got)
	}
	// Durante a passagem do brilho, alguma coluna tem que sair diferente.
	paint = WordmarkPainter(larg, 10*time.Second, SheenDuration/2, true)
	diferentes := 0
	for x := 0; x < larg; x++ {
		if paint('#', x, 0) != "#FEBF02" {
			diferentes++
		}
	}
	if diferentes == 0 {
		t.Error("o brilho não mudou nenhuma coluna")
	}
}

func TestSemAnimacaoFicaChapado(t *testing.T) {
	// Sem truecolor, ou com --sem-animacao, o wordmark tem que ficar âmbar
	// chapado e visível — nunca invisível.
	paint := WordmarkPainter(57, 0, 0, false)
	for x := 0; x < 57; x++ {
		if got := paint('#', x, 0); got != "#FEBF02" {
			t.Fatalf("coluna %d = %q, queria âmbar chapado", x, got)
		}
	}
}

func TestMascoteSobeEChega(t *testing.T) {
	const total = 14
	if n := MascotLinesVisible(total, 0, true); n != 0 {
		t.Errorf("no quadro 0 apareceram %d linhas, queria 0", n)
	}
	if n := MascotLinesVisible(total, IntroDuration, true); n != total {
		t.Errorf("no fim apareceram %d linhas, queria %d", n, total)
	}
	if n := MascotLinesVisible(total, 0, false); n != total {
		t.Errorf("sem animação deveria aparecer inteiro, veio %d", n)
	}
}

func TestPiscadaEBreveERara(t *testing.T) {
	// Uma piscada de 120ms a cada 7s: presente o bastante para dar vida, rara o
	// bastante para nao distrair. Se a proporcao virar, o mascote fica nervoso.
	piscando := 0
	const amostras = 7000
	for i := 0; i < amostras; i++ {
		if isBlinking(time.Duration(i) * time.Millisecond) {
			piscando++
		}
	}
	if piscando == 0 {
		t.Fatal("nunca pisca")
	}
	if fracao := float64(piscando) / amostras; fracao > 0.05 {
		t.Errorf("pisca em %.1f%% do tempo — está nervoso demais", fracao*100)
	}
}

func TestPiscadaFechaOsOlhos(t *testing.T) {
	aberto := BlinkingMascot(art.Mascot, 3*time.Second, true)
	fechado := BlinkingMascot(art.Mascot, 0, true)

	brancosAberto := strings.Count(strings.Join(aberto, ""), "W")
	brancosFechado := strings.Count(strings.Join(fechado, ""), "W")
	if brancosFechado >= brancosAberto {
		t.Errorf("piscando tem %d pixels claros e aberto tem %d — os olhos não fecharam",
			brancosFechado, brancosAberto)
	}
	// E a grade tem que continuar retangular, senão o desenho inteiro entorta.
	for i, l := range fechado {
		if len(l) != len(art.Mascot[i]) {
			t.Fatalf("a piscada mudou o tamanho da linha %d", i)
		}
	}
}

func TestRampaNaoEstoura(t *testing.T) {
	// Uma cor fora de #RRGGBB quebra o lipgloss em silêncio: ele descarta e o
	// pixel some.
	for i := 0; i <= 100; i++ {
		c := RampColor(float64(i) / 50) // passa de 1 de propósito
		if len(c) != 7 || c[0] != '#' {
			t.Fatalf("RampColor(%v) = %q, não é #RRGGBB", float64(i)/50, c)
		}
	}
}
