package console

// anim.go — o movimento do console.
//
// Tres regras que valem para tudo aqui:
//
//  1. Toda funcao e PURA: recebe o tempo decorrido e devolve o quadro. Nada de
//     relogio de parede por dentro. E o que permite testar a animacao pedindo o
//     quadro no instante N em vez de dormir e torcer.
//  2. Nada anima quando a saida nao e um console, quando --sem-animacao esta
//     ligado, ou durante um envio. Num disparo a atencao e da bar de
//     progresso; brilho competindo com ela e ruido.
//  3. Nenhuma animacao segura um envio. Se as duas disputarem, o envio ganha e o
//     quadro e descartado. (invariante 14)

import (
	"math"
	"strings"
	"time"
)

// Tempos da abertura. 28 quadros a 20 fps.
const (
	IntroDuration  = 1400 * time.Millisecond
	wordmarkReveal = 700 * time.Millisecond
	mascotEntry    = 800 * time.Millisecond
	mascotDelay    = 300 * time.Millisecond
	taglineEntry   = 700 * time.Millisecond

	// O brilho atravessa o wordmark uma vez a cada 20 s. Loop continuo numa
	// janela aberta o dia inteiro cansa a vista e queima CPU a toa.
	SheenCycle    = 20 * time.Second
	SheenDuration = 900 * time.Millisecond
	sheenBand     = 0.42 // fatia da rampa visivel de uma vez
)

// easeOutCubic desacelera no fim. Sem ela a revelacao parece um cursor andando;
// com ela parece uma coisa entrando em cena.
func easeOutCubic(t float64) float64 {
	t = clamp01(t)
	u := 1 - t
	return 1 - u*u*u
}

func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

// sheenRamp e o degrade metalico do wordmark: gold nas pontas, creme no
// brilho especular. Fora da passagem do brilho, o wordmark fica ambar chapado.
var sheenRamp = []struct {
	pos float64
	cor string
}{
	{0.00, "#C98F00"},
	{0.34, "#E7AD00"},
	{0.54, "#FEBF02"},
	{0.66, "#FFE07A"},
	{0.72, "#FFF5C8"},
	{0.82, "#FFD34D"},
	{1.00, "#C98F00"},
}

// RampColor interpola a rampa na posicao t (fora de [0,1] volta pelas pontas).
func RampColor(t float64) string {
	t = t - math.Floor(t)
	for i := 0; i < len(sheenRamp)-1; i++ {
		a, b := sheenRamp[i], sheenRamp[i+1]
		if t >= a.pos && t <= b.pos {
			k := 0.0
			if b.pos > a.pos {
				k = (t - a.pos) / (b.pos - a.pos)
			}
			return mixHex(a.cor, b.cor, k)
		}
	}
	return sheenRamp[len(sheenRamp)-1].cor
}

func mixHex(a, b string, k float64) string {
	ar, ag, ab := hexToRGB(a)
	br, bg, bb := hexToRGB(b)
	return rgbToHex(
		int(float64(ar)+(float64(br)-float64(ar))*k+0.5),
		int(float64(ag)+(float64(bg)-float64(ag))*k+0.5),
		int(float64(ab)+(float64(bb)-float64(ab))*k+0.5),
	)
}

func hexToRGB(h string) (int, int, int) {
	v := 0
	for _, c := range h[1:] {
		v = v*16 + hexDigit(byte(c))
	}
	return v >> 16 & 0xFF, v >> 8 & 0xFF, v & 0xFF
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}

func rgbToHex(r, g, b int) string {
	const d = "0123456789ABCDEF"
	out := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []int{r, g, b} {
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		out[1+i*2] = d[v>>4]
		out[2+i*2] = d[v&0xF]
	}
	return string(out)
}

// WordmarkPainter devolve o pintor do wordmark no instante dado.
//
// desdeAbertura serve para a revelacao coluna a coluna; desdeCiclo controla a
// passagem do brilho. animating=false devolve ambar chapado, que e o estado de
// repouso e tambem o que sobra quando o terminal nao tem cor de 24 bits.
func WordmarkPainter(largura int, desdeAbertura, desdeCiclo time.Duration, animating bool) func(byte, int, int) string {
	const chapado = "#FEBF02"
	if !animating {
		return func(ch byte, x, y int) string {
			if ch == '#' {
				return chapado
			}
			return ""
		}
	}

	// Revelacao: quantas colunas ja apareceram.
	reveladas := largura
	if desdeAbertura < wordmarkReveal {
		p := float64(desdeAbertura) / float64(wordmarkReveal)
		reveladas = int(easeOutCubic(p) * float64(largura))
	}

	// Brilho: fase da rampa, ou -1 quando esta em repouso.
	fase := -1.0
	if desdeAbertura < IntroDuration {
		fase = float64(desdeAbertura) / float64(wordmarkReveal)
	} else if r := desdeCiclo % SheenCycle; r < SheenDuration {
		fase = float64(r) / float64(SheenDuration)
	}

	return func(ch byte, x, y int) string {
		if ch != '#' || x >= reveladas {
			return ""
		}
		// A coluna da frente sai em creme e assenta em ambar: e o que da a
		// impressao de que a letra acabou de acender.
		if x >= reveladas-2 && desdeAbertura < wordmarkReveal {
			return "#FFF5C8"
		}
		if fase < 0 {
			return chapado
		}
		pos := float64(x)/float64(largura)*sheenBand + fase*(1+sheenBand) - sheenBand
		if pos < 0 || pos > 1 {
			return chapado
		}
		return RampColor(pos)
	}
}

// MascotLinesVisible diz quantas linhas do mascote ja entraram.
//
// Ele sobe de baixo para cima, uma faixa por quadro, comecando um pouco depois
// do wordmark para as duas entradas nao competirem pela atencao.
func MascotLinesVisible(total int, desdeAbertura time.Duration, animating bool) int {
	if !animating || desdeAbertura >= mascotDelay+mascotEntry {
		return total
	}
	if desdeAbertura < mascotDelay {
		return 0
	}
	p := float64(desdeAbertura-mascotDelay) / float64(mascotEntry)
	return int(easeOutCubic(p)*float64(total) + 0.5)
}

// TaglineFade devolve o quanto a tagline ja apareceu: 0 invisivel, 1 e 2
// intermediarios, 3 na cor final. Fade em tres passos porque terminal nao tem
// opacidade — o que da para fazer e escurecer a cor.
func TaglineFade(desdeAbertura time.Duration, animating bool) int {
	if !animating {
		return 3
	}
	inicio := IntroDuration - taglineEntry
	if desdeAbertura < inicio {
		return 0
	}
	p := float64(desdeAbertura-inicio) / float64(taglineEntry)
	return int(clamp01(p)*3 + 0.5)
}

// StatusPulse alterna a cor do ponto de conexao a 1 Hz. E o sinal de vida da
// tela quando nao ha nada acontecendo.
func StatusPulse(decorrido time.Duration, animating bool) string {
	if !animating || (decorrido/(500*time.Millisecond))%2 == 0 {
		return "#FEBF02"
	}
	return "#A87E00"
}

// BlinkingMascot devolve a grade do mascote com os olhos fechados durante a
// piscada. Sao quatro pixels alterados numa linha — e o que faz o desenho
// parecer vivo em vez de colado.
func BlinkingMascot(grade []string, decorrido time.Duration, animating bool) []string {
	if !animating || !isBlinking(decorrido) {
		return grade
	}
	fora := make([]string, len(grade))
	copy(fora, grade)
	// As linhas 11 e 12 da grade sao a altura dos olhos; trocar o branco por
	// preto ali fecha as palpebras.
	for _, y := range []int{11, 12} {
		if y < len(fora) {
			fora[y] = strings.ReplaceAll(fora[y], "W", "K")
		}
	}
	return fora
}

const (
	blinkEvery = 7 * time.Second
	blinkFor   = 120 * time.Millisecond
)

func isBlinking(decorrido time.Duration) bool {
	return decorrido%blinkEvery < blinkFor
}
