package main

import (
	"context"
	"github.com/wilamis-brasil/abraham/internal/command"
	"github.com/wilamis-brasil/abraham/internal/console"
	"github.com/wilamis-brasil/abraham/internal/dispatch"
	"github.com/wilamis-brasil/abraham/internal/store"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// Este arquivo cobre a costura que NAO tinha teste nenhum e por isso deixou o
// bot mudo por duas rodadas: evento -> comando -> resposta.
//
// A deteccao do self-chat esta em whatsapp_test.go. Aqui comeca depois dela: o
// comando ja foi reconhecido, e o que se testa e se ele vira acao e resposta.

func palcoDeTeste(t *testing.T) (*console.Output, *replier, *dispatch.Dispatcher, *waFake) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "dados", "abraham.db"))
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fake := novoFake()
	out := console.New(io.Discard, console.Options{Simples: true})
	t.Cleanup(out.Close)

	resp := newReplier(fake)
	d := dispatch.New(db, fake)
	d.MinInterval, d.MaxInterval = 0, 0
	d.OnResult = func(r dispatch.Result) { reportResult(out, resp, r) }

	return out, resp, d, fake
}

func TestFalhasRespondeSemEnvioAnterior(t *testing.T) {
	out, resp, d, fake := palcoDeTeste(t)

	handleCommand(context.Background(), out, resp, d, command.Command{Kind: command.Failures})
	resp.stop()

	ditas := fake.ditas()
	if len(ditas) != 1 {
		t.Fatalf("respostas = %#v, queria once", ditas)
	}
	if !strings.Contains(ditas[0], "Nenhum envio") {
		t.Errorf("resposta = %q", ditas[0])
	}
}

func TestCancelarSemEnvioResponde(t *testing.T) {
	out, resp, d, fake := palcoDeTeste(t)

	handleCommand(context.Background(), out, resp, d, command.Command{Kind: command.Cancel})
	resp.stop()

	ditas := fake.ditas()
	if len(ditas) != 1 || !strings.Contains(ditas[0], "Nenhum envio em andamento") {
		t.Fatalf("respostas = %#v", ditas)
	}
}

func TestEnviarAvisaNoComecoENoFim(t *testing.T) {
	out, resp, d, fake := palcoDeTeste(t)

	lista := numeros(3)
	handleCommand(context.Background(), out, resp, d,
		command.Command{Kind: command.Send, Targets: alvos(lista, "oi"), Blocks: 1})
	d.Wait()
	resp.stop()

	ditas := fake.ditas()
	if len(ditas) != 2 {
		t.Fatalf("respostas = %#v, queria duas (inicio e done)", ditas)
	}
	if !strings.Contains(ditas[0], "Enviando para 3") {
		t.Errorf("primeira resposta = %q", ditas[0])
	}
	if !strings.Contains(ditas[1], "3 mensagens enviadas") {
		t.Errorf("resposta final = %q", ditas[1])
	}
	// E as mensagens de verdade sairam.
	if n := len(fake.tentativas()); n != 3 {
		t.Errorf("tentativas = %d, queria 3", n)
	}
	// O conteudo da mensagem nunca pode aparecer numa resposta administrativa.
	for _, d := range ditas {
		if strings.Contains(d, "oi") {
			t.Errorf("o conteudo vazou para a resposta: %q", d)
		}
	}
}

func TestSegundoEnviarRespondeQueJaTemUm(t *testing.T) {
	out, resp, d, fake := palcoDeTeste(t)

	segura := make(chan struct{})
	fake.aoEnviar = func(string, int) { <-segura }

	ctx := context.Background()
	handleCommand(ctx, out, resp, d, command.Command{Kind: command.Send, Targets: alvos(numeros(3), "a"), Blocks: 1})
	esperar(t, d.Running)
	handleCommand(ctx, out, resp, d, command.Command{Kind: command.Send, Targets: alvos(numeros(2), "b"), Blocks: 1})

	close(segura)
	d.Wait()
	resp.stop()

	juntas := strings.Join(fake.ditas(), "\n")
	if !strings.Contains(juntas, "Já existe um envio em andamento") {
		t.Errorf("o segundo /enviar não foi avisado: %q", juntas)
	}
}

func TestRespostasSaemNaOrdem(t *testing.T) {
	// O /falhas com muitos números responde em blocos, e eles só fazem sentido
	// na sequência. A queue tem um consumidor só justamente por isso.
	_, resp, _, fake := palcoDeTeste(t)

	for _, t := range []string{"um", "dois", "tres", "quatro", "cinco"} {
		resp.say(t)
	}
	resp.stop()

	quer := []string{"um", "dois", "tres", "quatro", "cinco"}
	ditas := fake.ditas()
	if len(ditas) != len(quer) {
		t.Fatalf("respostas = %#v", ditas)
	}
	for i := range quer {
		if ditas[i] != quer[i] {
			t.Fatalf("posição %d = %q, queria %q — a ordem não foi preservada", i, ditas[i], quer[i])
		}
	}
}

func TestDoisBlocosDePontaAPonta(t *testing.T) {
	// A prova do recurso novo atravessando tudo: parser, despacho, e a resposta
	// que o usuario le no WhatsApp.
	out, resp, d, fake := palcoDeTeste(t)

	cmd, err := command.Parse(
		"/enviar\n5511900000001 Maria\n5511900000002\n\nReunião de pais na sexta.\n\n" +
			"/enviar\n5511900000003 João\n\nOlá {nome}, o boletim saiu.")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	handleCommand(context.Background(), out, resp, d, cmd)
	d.Wait()
	resp.stop()

	// Cada grupo recebeu o seu text.
	quer := []string{
		"Reunião de pais na sexta.",
		"Reunião de pais na sexta.",
		"Olá João, o boletim saiu.",
	}
	textos := fake.textos()
	if len(textos) != 3 {
		t.Fatalf("textos = %#v, queria 3", textos)
	}
	for i := range quer {
		if textos[i] != quer[i] {
			t.Errorf("destinatário %d recebeu %q, queria %q", i, textos[i], quer[i])
		}
	}

	// E a resposta diz que foram duas mensagens diferentes — sem isso a pessoa
	// não tem como conferir que o comando foi lido do jeito que ela escreveu.
	ditas := fake.ditas()
	if len(ditas) == 0 {
		t.Fatal("nenhuma resposta")
	}
	if !strings.Contains(ditas[0], "3 contatos em 2 mensagens diferentes") {
		t.Errorf("aviso de início = %q", ditas[0])
	}
}

func TestRepetidoEntreBlocosApareceNaResposta(t *testing.T) {
	out, resp, d, fake := palcoDeTeste(t)

	cmd, err := command.Parse(
		"/enviar\n5511900000001\n\nPrimeira.\n\n" +
			"/enviar\n5511900000001\n5511900000002\n\nSegunda.")
	if err != nil {
		t.Fatal(err)
	}
	handleCommand(context.Background(), out, resp, d, cmd)
	d.Wait()
	resp.stop()

	if !strings.Contains(fake.ditas()[0], "1 número repetido foi ignorado") {
		t.Errorf("aviso = %q; o repetido tem que aparecer, senão a pessoa acha que mandou para 3",
			fake.ditas()[0])
	}
	if n := len(fake.tentativas()); n != 2 {
		t.Errorf("tentativas = %d, queria 2", n)
	}
}
