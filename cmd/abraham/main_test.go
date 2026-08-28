package main

import (
	"errors"
	"github.com/wilamis-brasil/abraham/internal/command"
	"github.com/wilamis-brasil/abraham/internal/console"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstanciaUnica(t *testing.T) {
	// Nome proprio: com o mutex de producao, este teste falharia sempre que o
	// bot estivesse aberto na maquina do desenvolvedor.
	nome := `Local\AbrahamTeste-` + t.Name()

	// A segunda copia tem que ser recusada, e recusada com o motivo certo: quem
	// da dois cliques no atalho merece "ja esta aberto", nao um erro tecnico.
	unlock, err := lockInstance(nome)
	if err != nil {
		t.Fatalf("primeira instancia deveria travar: %v", err)
	}
	defer unlock()

	if _, err := lockInstance(nome); !errors.Is(err, errAlreadyRunning) {
		t.Fatalf("segunda instancia = %v, queria errAlreadyRunning", err)
	}

	// E depois de unlock, tem que dar para abrir de novo. Se o mutex ficasse
	// preso, o bot nao reabriria ate reiniciar a maquina.
	unlock()
	liberar2, err := lockInstance(nome)
	if err != nil {
		t.Fatalf("depois de unlock deveria travar de novo: %v", err)
	}
	liberar2()
}

func TestMascararNaoVazaNumero(t *testing.T) {
	casos := map[string]string{
		"5511987654321": "5511*****4321",
		"551198765432":  "5511****5432",
		"123":           "****",
	}
	for entrada, quer := range casos {
		if got := console.MaskPhone(entrada); got != quer {
			t.Errorf("console.MaskPhone(%q) = %q, queria %q", entrada, got, quer)
		}
	}
	// O miolo nunca pode sobreviver.
	if strings.Contains(console.MaskPhone("5511987654321"), "9876") {
		t.Error("o miolo do telefone vazou")
	}
}

func TestPruneOldLogs(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "2026-01-01.log")
	recent := filepath.Join(dir, "2026-08-27.log")
	other := filepath.Join(dir, "leiame.txt")
	for _, f := range []string{old, recent, other} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().AddDate(0, 0, -45)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	if err := pruneOldLogs(dir, 30); err != nil {
		t.Fatalf("pruneOldLogs: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the 45-day-old log was not deleted")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("a recent log was deleted")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("a file that is not a log was deleted")
	}
}

func TestPruneLogsWithoutFolder(t *testing.T) {
	// First run of the program: the logs folder does not exist yet. That is not
	// an error.
	if err := pruneOldLogs(filepath.Join(t.TempDir(), "nao-existe"), 30); err != nil {
		t.Errorf("a missing folder should be silent, got: %v", err)
	}
}

func TestAvisoDeInicio(t *testing.T) {
	umBloco := func(n int) command.Command {
		return command.Command{Kind: command.Send, Targets: make([]command.Target, n), Blocks: 1}
	}

	curto := startNotice(umBloco(3), 12*time.Second)
	if !strings.Contains(curto, "3 contatos") {
		t.Errorf("= %q", curto)
	}
	// Envio de segundos não merece previsão: seria ruído.
	if strings.Contains(curto, "minutos") {
		t.Errorf("envio curto não deveria prever duração: %q", curto)
	}

	longo := startNotice(umBloco(500), 33*time.Minute)
	if !strings.Contains(longo, "33 minutos") {
		t.Errorf("= %q", longo)
	}
	if !strings.Contains(longo, "janela aberta") {
		t.Errorf("faltou o aviso de não fechar a janela: %q", longo)
	}

	// Vários blocos: a pessoa precisa conferir que o comando foi lido do jeito
	// que ela escreveu.
	varios := command.Command{
		Kind: command.Send, Targets: make([]command.Target, 5), Blocks: 3, Skipped: 2,
	}
	notice := startNotice(varios, 0)
	if !strings.Contains(notice, "5 contatos em 3 mensagens diferentes") {
		t.Errorf("= %q", notice)
	}
	if !strings.Contains(notice, "2 números repetidos foram ignorados") {
		t.Errorf("os repetidos não apareceram: %q", notice)
	}
}
