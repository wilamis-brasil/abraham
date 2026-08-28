package main

// Command abraham is a local WhatsApp bulk sender driven from the self-chat.
//
// This file decides nothing about the product. It wires the pieces together,
// guarantees only one Abraham is running, and turns events into actions. If a
// business rule shows up here, it is in the wrong place.
//
// Windows only, on purpose. The deliverable is one abraham.exe; portability
// nobody will use would be code written "for the future".

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/wilamis-brasil/abraham/internal/command"
	"github.com/wilamis-brasil/abraham/internal/console"
	"github.com/wilamis-brasil/abraham/internal/dispatch"
	"github.com/wilamis-brasil/abraham/internal/store"
	"github.com/wilamis-brasil/abraham/internal/whatsapp"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const version = "0.1.0-dev"

// mutexName identifies the process to Windows. "Global\\" would need
// administrator rights; "Local\\" is enough, since the bot runs inside the
// user's session.
const mutexName = `Local\AbrahamBotWhatsApp`

func main() {
	var (
		plain       = flag.Bool("simples", false, "glifos e cores mínimos, sem painel")
		noAnimation = flag.Bool("sem-animacao", false, "mantém as cores, desliga o movimento")
		showVersion = flag.Bool("versao", false, "mostra a versão e sai")
		showHelp    = flag.Bool("ajuda", false, "explica o que é e como usar")
	)
	flag.Usage = func() { console.PrintHelp(version) }
	flag.Parse()

	switch {
	case *showVersion:
		console.PrintVersion(version)
		return
	case *showHelp:
		console.PrintHelp(version)
		return
	}

	// Clicar direto no abraham.exe tem que abrir como aplicativo, não como
	// sessão de terminal: título próprio e acentuação certa na tela.
	console.SetupWindow()

	// The classic Windows console only understands ANSI escapes after this.
	restore := console.EnableVT()
	defer restore()

	out := console.NewStdout(console.Options{
		Simples:     *plain,
		SemAnimacao: *noAnimation,
		Version:     version,
	})
	defer out.Close()

	if err := run(out); err != nil {
		out.Fail(err.Error())
		out.Close()
		restore()
		os.Exit(1)
	}
}

func run(out *console.Output) error {
	base, err := programFolder()
	if err != nil {
		return err
	}

	// Single instance before anything else: two copies touching the same
	// database and the same WhatsApp session is the worst failure available
	// here.
	unlock, err := lockInstance(mutexName)
	if errors.Is(err, errAlreadyRunning) {
		out.Warn("O Abraham já está aberto. Procure a janela que já está rodando.")
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()

	logFolder := filepath.Join(base, "logs")
	_ = pruneOldLogs(logFolder, 30)
	closeLog, err := openLog(logFolder)
	if err != nil {
		return err
	}
	defer closeLog()
	log.Printf("INFO  abraham iniciado version=%s", version)

	out.Headline("Abraham " + version)

	db, err := store.Open(filepath.Join(base, "dados", "abraham.db"))
	if err != nil {
		log.Printf("ERRO  abrir banco: %v", err)
		return errors.New("não consegui abrir os dados locais. Veja a pasta logs.")
	}
	defer db.Close()
	out.Step("dados locais carregados")

	if db.Salvaged != "" {
		log.Printf("WARN  base corrompida preservada em %s", db.Salvaged)
		out.Warn("Os dados locais estavam danificados. Uma cópia foi preservada.")
		out.Note("Você vai precisar conectar o WhatsApp de novo.")
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// A job left marked "running" means the process died mid-send. It is closed
	// as interrupted and NEVER resumed.
	if summary, err := db.RecoverInterrupted(ctx); err != nil {
		log.Printf("ERRO  recuperacao: %v", err)
	} else if summary != nil {
		log.Printf("WARN  envio interrompido job=%d enviados=%d nao_tentados=%d incertos=%d",
			summary.JobID, summary.Sent, summary.NotTried, summary.Uncertain)
		out.Interrupted(*summary)
	}

	device, err := whatsapp.OpenSession(ctx, db.SQL, store.Dialect)
	if err != nil {
		log.Printf("ERRO  sessao: %v", err)
		return errors.New("não consegui preparar a sessão do WhatsApp. Veja a pasta logs.")
	}
	client := whatsapp.New(device)
	defer client.Close()

	// Proxy for connectivity only, for networks that block WhatsApp's ports.
	// The URL never reaches the log: it may carry a username and password.
	if endereco := os.Getenv("ABRAHAM_PROXY"); endereco != "" {
		if err := client.UseProxy(endereco); err != nil {
			log.Printf("ERRO  proxy invalido: %v", err)
			return errors.New("o endereço em ABRAHAM_PROXY não é válido. Veja a pasta logs.")
		}
		log.Printf("INFO  conectando por proxy")
		out.Note("conectando pelo proxy configurado em ABRAHAM_PROXY")
	}

	if client.Number() == "" {
		out.Working("primeira vez — vou pedir o código de conexão")
	} else {
		out.Step("sessão do WhatsApp encontrada")
	}

	resp := newReplier(client)
	defer resp.stop()

	dispatcher := dispatch.New(db, client)
	dispatcher.OnProgress = out.Progress
	dispatcher.OnResult = func(r dispatch.Result) { reportResult(out, resp, r) }

	out.Working("conectando ao WhatsApp")
	if err := client.Connect(ctx); err != nil {
		log.Printf("ERRO  conectar: %v", err)
		return errors.New("não consegui conectar ao WhatsApp. Veja a pasta logs.")
	}

	loop(ctx, out, client, resp, dispatcher)

	// Shutdown: cancel the running send and wait for it to close its state in
	// the database, or the next boot would find a half-written job.
	if dispatcher.Running() {
		out.Working("encerrando o envio em andamento")
		dispatcher.Cancel()
		dispatcher.Wait()
	}
	log.Printf("INFO  abraham encerrado")
	out.Note("até logo.")
	return nil
}

// replier queues up the replies that go back over WhatsApp.
//
// A single goroutine consumes it, for two reasons:
//
//   - ORDER is preserved, and /falhas with many numbers depends on it: the
//     answer comes in chunks that only make sense in sequence;
//   - the event loop stops blocking. Before this, every reply held the loop for
//     up to 15 seconds on a network call, and anything arriving in that window
//     was dropped. A /falhas sent right after a /enviar simply vanished.
type replier struct {
	queue chan string
	done  chan struct{}
	once  sync.Once
}

// replyTarget is all the reply queue needs. A minimal interface declared in the
// consumer, so the end-to-end test can hand it a fake.
type replyTarget interface {
	Reply(ctx context.Context, text string) error
}

func newReplier(wa replyTarget) *replier {
	r := &replier{queue: make(chan string, 64), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		for text := range r.queue {
			ctx, cancelar := context.WithTimeout(context.Background(), 15*time.Second)
			err := wa.Reply(ctx, text)
			cancelar()
			if err != nil {
				log.Printf("ERRO  resposta nao enviada: %v", err)
			} else {
				log.Printf("INFO  resposta enviada")
			}
		}
	}()
	return r
}

// say queues a reply. Never blocks the caller on the network.
func (r *replier) say(text string) {
	select {
	case r.queue <- text:
	default:
		// 64 replies pending means WhatsApp stopped accepting. Recording that
		// beats growing without bound.
		log.Printf("WARN  resposta descartada, fila cheia")
	}
}

// stop waits for the queue to drain: a send's final reply has to go out.
func (r *replier) stop() {
	r.once.Do(func() { close(r.queue) })
	<-r.done
}

// loop turns events into actions. It is the heart of the program, and it fits
// on one screen.
func loop(ctx context.Context, out *console.Output, client *whatsapp.Client, resp *replier, d *dispatch.Dispatcher) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, aberto := <-client.Events():
			if !aberto {
				return
			}
			handleEvent(ctx, out, client, resp, d, ev)
		}
	}
}

func handleEvent(ctx context.Context, out *console.Output, client *whatsapp.Client, resp *replier, d *dispatch.Dispatcher, ev whatsapp.Event) {
	switch ev.Kind {
	case whatsapp.QRCode:
		out.QR(ev.QR) // the code never reaches the log (invariant 13)

	case whatsapp.Confirmation:
		// The handoff code is not a credential: it exists precisely to be read
		// aloud and compared. It can appear on screen.
		log.Printf("INFO  conferencia de codigo solicitada")
		out.Headline("confira o código")
		out.Note("o celular está mostrando este código:")
		out.PairingCode(ev.Text)
		out.Note("se for igual ao do celular, está tudo certo — já confirmei aqui.")

	case whatsapp.Paired:
		log.Printf("INFO  pareado")
		out.Step("celular conectado")

	case whatsapp.Connected:
		d.SetOnline(true)
		log.Printf("INFO  whatsapp conectado")
		out.Step("conectado ao WhatsApp")
		out.Connected(client.Number())

	case whatsapp.Disconnected:
		d.SetOnline(false)
		log.Printf("WARN  whatsapp desconectado")
		out.Disconnected()
		out.Warn("conexão caiu — tentando voltar")

	case whatsapp.LoggedOut:
		d.SetOnline(false)
		out.Disconnected()
		log.Printf("WARN  sessao encerrada: %s", ev.Text)
		out.Warn("a sessão do WhatsApp foi encerrada. Feche e abra o Abraham para conectar de novo.")
		out.Note("motivo: " + ev.Text)

	case whatsapp.Banned:
		// The right answer is to stop. Never work around it, never wait and try
		// again at a different pace.
		log.Printf("ERRO  banimento temporario motivo=%s ate=%s", ev.Text, ev.Until.Format(time.RFC3339))
		d.Block("o WhatsApp aplicou uma restrição temporária a esta conta")
		out.Fail("o WhatsApp restringiu esta conta temporariamente. Os envios foram parados.")

	case whatsapp.CommandError:
		log.Printf("INFO  comando recusado")
		resp.say(ev.Text)

	case whatsapp.CommandKind:
		handleCommand(ctx, out, resp, d, ev.Cmd)
	}
}

func handleCommand(ctx context.Context, out *console.Output, resp *replier, d *dispatch.Dispatcher, cmd command.Command) {
	switch cmd.Kind {
	case command.Send:
		total := len(cmd.Targets)
		if err := d.Start(ctx, cmd.Targets); err != nil {
			log.Printf("INFO  envio recusado: %v", err)
			if errors.Is(err, dispatch.ErrBusy) {
				resp.say("Já existe um envio em andamento. Use /cancelar para parar.")
			} else {
				resp.say("Não consegui iniciar o envio. " + err.Error())
			}
			return
		}
		log.Printf("INFO  envio iniciado total=%d", total)
		out.SendStarted(total)
		resp.say(startNotice(cmd, d.EstimatedDuration(total)))

	case command.Cancel:
		if !d.Cancel() {
			resp.say("Nenhum envio em andamento.")
			return
		}
		log.Printf("INFO  envio cancelado pelo usuario")
		// O summary sai no OnResultado, quando o worker realmente parar.

	case command.Failures:
		msgs, err := d.FailuresText(ctx)
		if err != nil {
			log.Printf("ERRO  /falhas: %v", err)
			resp.say("Não consegui consultar as falhas agora.")
			return
		}
		for _, m := range msgs {
			resp.say(m)
		}
	}
}

// startNotice says how many and how long.
//
// The estimate matters: with the 1-7s randomised wait, 500 messages run past
// half an hour. Someone who does not know that closes the window half-way
// through thinking it froze, and the send stops with it.
func startNotice(cmd command.Command, duration time.Duration) string {
	notice := fmt.Sprintf("Enviando para %d contatos", len(cmd.Targets))
	if cmd.Blocks > 1 {
		notice += fmt.Sprintf(" em %d mensagens diferentes", cmd.Blocks)
	}
	notice += "..."
	// A number repeated across blocks is nearly always a slip by whoever built
	// the list, so it is worth saying — without turning it into an error,
	// because the send itself is fine.
	switch {
	case cmd.Skipped == 1:
		notice += "\n1 número repetido foi ignorado."
	case cmd.Skipped > 1:
		notice += fmt.Sprintf("\n%d números repetidos foram ignorados.", cmd.Skipped)
	}
	if duration >= time.Minute {
		notice += fmt.Sprintf("\nDeve levar uns %d minutos. Pode deixar a janela aberta.",
			int(duration.Round(time.Minute).Minutes()))
	}
	return notice
}

// reportResult closes a send: one line on screen, one in the log, one on
// WhatsApp.
func reportResult(out *console.Output, resp *replier, r dispatch.Result) {
	log.Printf("INFO  envio finalizado job=%d estado=%s enviadas=%d falhas=%d duracao=%s",
		r.JobID, r.Status, r.Sent, r.Failed, r.Duration.Round(time.Second))

	var text string
	switch {
	case r.Status == store.JobCancelled:
		text = fmt.Sprintf("Envio cancelado.\n%d enviadas · %d não enviadas.", r.Sent, r.Failed)
	case r.Status == store.JobInterrupted:
		text = fmt.Sprintf("Envio interrompido.\n%d enviadas · %d não enviadas.\nUse /falhas.", r.Sent, r.Failed)
	case r.Failed > 0:
		text = fmt.Sprintf("%d enviadas · %d não enviadas.\nUse /falhas para ver os números.", r.Sent, r.Failed)
	default:
		text = fmt.Sprintf("%d mensagens enviadas.", r.Sent)
	}

	out.SendFinished(r)
	resp.say(text)
}

// ------------------------------------------------------------------ suporte

// pruneOldLogs deletes log files older than the given number of days.
//
// No log-rotation dependency: a bot running on a school's front-desk machine
// needs nothing more than deleting old files at startup.
func pruneOldLogs(dir string, dias int) error {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	limite := time.Now().AddDate(0, 0, -dias)
	for _, e := range entradas {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(limite) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// programFolder is the executable's folder, not the working directory. A
// shortcut can set any working directory, but dados/ and logs/ have to sit
// next to the program.
func programFolder() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("não consegui descobrir a pasta do programa: %w", err)
	}
	return filepath.Dir(exe), nil
}

// errAlreadyRunning separates "another copy is open" from "the mutex failed".
// Different situations: the first is normal and deserves a friendly notice, the
// second is a defect and has to reach the log.
var errAlreadyRunning = errors.New("já em execução")

// lockInstance creates a named Windows mutex.
//
// Better than a lock file: the operating system releases the mutex when the
// process dies, including when it dies badly. A lock file left behind by a
// power cut would keep the program from ever opening again.
//
// Mind the API: CreateMutex returns a VALID handle together with the
// ERROR_ALREADY_EXISTS error. Treating every err != nil as "already open" would
// hide real failures behind a reassuring message.
//
// The name is a parameter so the test does not fight the production mutex. With
// a fixed name the suite failed whenever the bot happened to be open on the
// machine — a test that only passes with the product closed is worthless.
func lockInstance(name string) (func(), error) {
	wide, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, wide)
	alreadyThere := errors.Is(err, windows.ERROR_ALREADY_EXISTS)

	if err != nil && !alreadyThere {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("não consegui reservar a instância única: %w", err)
	}
	if alreadyThere {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, errAlreadyRunning
	}
	return func() { windows.CloseHandle(h) }, nil
}

// openLog sends the log to logs/YYYY-MM-DD.log.
//
// What never goes in: the QR, session keys, message content, full phone lists.
// Phone numbers appear masked. (invariant 13)
func openLog(pasta string) (func(), error) {
	if err := os.MkdirAll(pasta, 0o755); err != nil {
		return nil, fmt.Errorf("não consegui criar a pasta de logs: %w", err)
	}
	nome := filepath.Join(pasta, time.Now().Format("2006-01-02")+".log")
	f, err := os.OpenFile(nome, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("não consegui abrir o arquivo de log: %w", err)
	}
	log.SetOutput(f)
	log.SetFlags(log.Ltime)
	return func() { _ = f.Close() }, nil
}
