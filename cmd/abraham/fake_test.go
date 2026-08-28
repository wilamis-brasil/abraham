package main

// fake_test.go — um WhatsApp de mentira.
//
// E o que torna possivel testar dispatch.go inteiro sem once conta real: envio
// normal, falha parcial, cancelamento no meio, queda de conexao. Nenhuma dessas
// situacoes daria para reproduzir de proposito contra o WhatsApp de verdade.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/wilamis-brasil/abraham/internal/command"
	"github.com/wilamis-brasil/abraham/internal/dispatch"
)

type waFake struct {
	mu sync.Mutex

	// enviados guarda os numeros, na ordem, para o teste conferir sequencia.
	enviados []string
	// enviadosTexto guarda o text que cada um recebeu, para conferir o {nome}.
	enviadosTexto []string
	// respostas guarda o que o bot escreveu no self-chat.
	respostas []string

	// falhar decide, por numero, se a chamada deve dar erro.
	falhar map[string]error
	// atraso simula latencia de rede.
	atraso time.Duration
	// aoEnviar roda antes de cada envio. E o gancho para o teste cancelar no
	// meio, derrubar a conexao, etc.
	aoEnviar func(numero string, n int)
}

func novoFake() *waFake {
	return &waFake{falhar: map[string]error{}}
}

func (f *waFake) Send(ctx context.Context, numero, text string) error {
	f.mu.Lock()
	n := len(f.enviados)
	gancho := f.aoEnviar
	atraso := f.atraso
	err := f.falhar[numero]
	f.enviados = append(f.enviados, numero)
	f.enviadosTexto = append(f.enviadosTexto, text)
	f.mu.Unlock()

	if gancho != nil {
		gancho(numero, n)
	}
	if atraso > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(atraso):
		}
	}
	return err
}

func (f *waFake) Reply(ctx context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respostas = append(f.respostas, text)
	return nil
}

func (f *waFake) Number() string { return "5511999999999" }

// textos devolve o text que cada destinatario recebeu, na ordem.
func (f *waFake) textos() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.enviadosTexto...)
}

// ditas devolve o que o bot respondeu no self-chat, na ordem.
func (f *waFake) ditas() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.respostas...)
}

func (f *waFake) tentativas() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.enviados...)
}

// alvos monta destinatarios com o mesmo text, que e o caso da maioria dos
// testes de despacho: o que se testa aqui e ritmo e cancelamento, nao
// personalizacao — essa tem os testes dela no pacote command.
func alvos(numeros []string, text string) []command.Target {
	out := make([]command.Target, len(numeros))
	for i, n := range numeros {
		out[i] = command.Target{Number: n, Text: text}
	}
	return out
}

func numeros(n int) []string {
	lista := make([]string, n)
	for i := range lista {
		lista[i] = fmt.Sprintf("55119%08d", i)
	}
	return lista
}

var (
	_ dispatch.Sender = (*waFake)(nil)
	_ replyTarget     = (*waFake)(nil)
)

// esperar aguarda once condicao ficar verdadeira. Evita sleep fixo, que deixa a
// suite lenta e instavel ao mesmo tempo.
func esperar(t *testing.T, cond func() bool) {
	t.Helper()
	limite := time.Now().Add(3 * time.Second)
	for time.Now().Before(limite) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("a condicao nao aconteceu em 3s")
}
