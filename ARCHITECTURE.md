# Arquitetura

Para quem — pessoa ou IA — vai mexer nisto daqui a dois anos. São dez minutos e
economizam horas.

---

## O fluxo, em uma página

```
                    WHATSAPP
                       │  eventos
                       ▼
          ┌──────────────────────────┐
          │  internal/whatsapp       │  ADAPTADOR
          │  a única fronteira com   │
          │  a biblioteca whatsmeow  │
          └────────────┬─────────────┘
                       │
       é mensagem da própria conta?  ─── não ──► ignora
                       │ sim
       está no self-chat?            ─── não ──► ignora
                       │ sim
       foi este processo que mandou? ─── sim ──► ignora
                       │ não
                       ▼
          ┌──────────────────────────┐
          │  internal/command        │  /enviar · /cancelar · /falhas
          └────────────┬─────────────┘
                       │ []Target{Numero, Texto}
                       ▼
          ┌──────────────────────────┐    ┌──────────────────┐
          │  internal/dispatch       │───►│  internal/store  │  SQLite
          │  1 envio · 1 número/vez  │    └──────────────────┘
          └────────────┬─────────────┘
                       │ Progress / Result
                       ▼
          ┌──────────────────────────┐
          │  internal/console        │  ADAPTADOR
          │  a única fronteira com   │
          │  o terminal              │
          └──────────────────────────┘
```

Os três filtros antes de virar comando não são paranoia:

1. **`IsFromMe`** — cliente não comanda o bot.
2. **Self-chat** — um `/enviar` escrito na conversa de um responsável mostraria
   os números dos outros destinatários para ele **antes** de o bot ler o
   comando. Apagar depois não resolve: a notificação já saiu.
3. **ID criado por este processo** — sem isso, um disparo cujo texto comece com
   `/enviar` se dispararia de novo, em loop.

---

## Por que pastas, se a pesquisa original proibia

A pesquisa que originou este projeto dizia, com todas as letras, para **não**
criar `internal/`, `domain/`, `usecases/` — que para um produto de três comandos
isso pioraria a manutenção.

**Ela estava certa, para 500 linhas.** O projeto tinha cinco arquivos planos.

Hoje são ~3.000 linhas de produção, e só a camada de tela passa de 1.400. O
argumento virou de lado: com tudo plano, quem chega precisa ler nove arquivos
para descobrir onde uma mensagem é enviada.

O que **continua valendo** da decisão original é o critério: cada pasta aqui é
uma fronteira que já existia no código, não uma camada inventada. Não há
`usecases/`, não há `repositories/`, não há fábrica. **Se a vontade de criar uma
aparecer, ela é a resposta errada para uma pergunta que ninguém fez.**

### A regra de dependência

```
cmd/abraham ──► console ──► dispatch ──► store
            ──► whatsapp ──► command
                               ▲
                               └── dispatch
```

`whatsapp` **não importa** `dispatch`. Ele satisfaz `dispatch.Sender` por
interface implícita, que é como Go faz porta e adaptador sem cerimônia. A
interface mora em quem **consome**, e tem um método só:

```go
type Sender interface {
    Send(ctx context.Context, number, text string) error
}
```

Uma interface menor é um fake menor. É ela que permite testar o despacho inteiro
— cancelamento no meio, falha parcial, queda de conexão — sem uma conta de
WhatsApp.

O `console` também importa `store`, para as constantes de estado. Uma aresta a
mais e zero código; a alternativa seria um enum mapeado, que é a cerimônia que
se está evitando.

### Os quatro padrões, e por que cada um é real

| Padrão | Onde | Por que existe |
|---|---|---|
| Porta e adaptador | `dispatch.Sender` ← `whatsapp.Client` / fake | duas implementações, e os testes usam a segunda |
| Escritor único | `console.Output` | uma goroutine dona do estado; o resto manda mutação por canal |
| Observador | `OnProgress` / `OnResult` | o envio não sabe o que é uma tela |
| Máquina de estados | trabalho e destinatário | desenhada abaixo, e é o que impede reenvio duplicado |

---

## Onde mexer

| Sintoma | Pacote |
|---|---|
| "Quero mudar como o `/enviar` é lido" | `internal/command` |
| "O WhatsApp parou de conectar" | `internal/whatsapp` |
| "O cancelamento está errado" | `internal/dispatch` |
| "Deu problema no banco" | `internal/store` |
| "A tela está feia ou quebrada" | `internal/console` |
| "A animação está estranha" | `internal/console/animation.go` |
| "Quero mudar o mascote" | `internal/console/art` |
| "O programa não inicia" | `cmd/abraham` |

---

## O estado de um envio

```
        /enviar válido
              │
              ▼
         ┌─────────┐
         │ running │
         └────┬────┘
              │
   ┌──────────┼────────────┬──────────────┐
   ▼          ▼            ▼              ▼
completed  with_errors  cancelled    interrupted
                                     (queda, ou o processo morreu)
```

Não existe `idle` no banco: **ausência de trabalho `running` = ocioso.**

E cada destinatário:

```
pending ──► sending ──► sent
                   └──► failed
```

Os dois passos existem por um motivo específico. Se a máquina desligar entre
eles, o que ficou em `sending` **pode ou não** ter chegado ao servidor. Na volta:

```
sending antigo  →  uncertain   (resultado incerto)
pending antigo  →  not_sent    (nem chegou a ser tentado)
trabalho        →  interrupted
```

**Nada é retomado.** Retomar duplicaria mensagem para uma pessoa real num caso
que o programa não tem como desambiguar. O `/falhas` devolve a lista e a decisão
é de quem opera.

---

## O banco

Um arquivo, `dados/abraham.db`, com as tabelas do whatsmeow e duas nossas:

```
whatsmeow_*      sessão e chaves (a biblioteca cuida)

app_jobs         id · estado · total · criado_em · finalizado_em
app_recipients   id · job_id · posicao · numero · estado · motivo
```

**O texto da mensagem não é gravado, nem o nome do destinatário.** O `/falhas`
precisa de números e estados, não de conteúdo. Guardar o texto criaria um
arquivo com o histórico de tudo que a escola já mandou, sem nenhum uso.

### A DSN não é decorativa

```go
"file:" + caminho + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
```

O `Container.Upgrade()` do whatsmeow lê `PRAGMA foreign_keys` por uma conexão
**qualquer do pool** e aborta se vier desligado. Um `PRAGMA` avulso depois do
`Open` pegaria em uma conexão só. A sintaxe é específica do driver: o `modernc`
usa `_pragma=`, o `mattn` usaria `?_foreign_keys=on`.

`internal/whatsapp/sqlite_test.go` existe para provar isso e continuar provando:
no dia em que uma atualização do whatsmeow quebrar a stack sem CGo, é ali que
aparece primeiro.

---

## A identidade da conta: telefone e LID

O WhatsApp está migrando de `@s.whatsapp.net` para `@lid`, e uma conta pode ter
os dois. **Isto já quebrou o produto inteiro duas vezes**, em silêncio: o bot
recebia os comandos e os descartava sem erro nenhum.

A regra que funciona não precisa saber qual identidade está valendo:

```go
// numa mensagem minha, Sender é quem enviou (eu) e Chat é para quem foi.
// Se os dois são a mesma pessoa, a conversa é comigo.
Chat.User == Sender.User
```

As identidades guardadas ficam como segunda via, para os caminhos do protocolo
que deixam `Sender` vazio. E a resposta volta pelo **endereço exato** de onde o
comando veio, nunca por um JID remontado.

---

## O ícone do executável

`cmd/abraham/rsrc_windows_*.syso` é gerado a partir de `cmd/abraham/winres/`
pelo [go-winres](https://github.com/tc-hib/go-winres) e **fica versionado** —
`go build` o inclui sozinho, sem instalar nada extra. Só mexe em quem for
trocar o ícone:

```bash
go install github.com/tc-hib/go-winres@latest
cd cmd/abraham && go-winres make
```

## Como atualizar o whatsmeow

Ele não tem release estável — o `go.mod` fixa uma pseudo-versão por commit.
**Nunca rode `go get -u`.**

Quando precisar atualizar, porque quebrou e não porque saiu versão nova:

1. `go get go.mau.fi/whatsmeow@<commit>`
2. `go test ./...` — o smoke test do SQLite é o primeiro a reclamar
3. Parear com um celular de verdade e disparar para um número de teste
4. Só então gerar o `abraham.exe` novo, com backup de `dados/` antes de trocar

Versão que está funcionando **não se mexe**.

---

## Os 14 invariantes

Quebrar qualquer um destes muda o produto, não o código.

```
 1. Existem apenas 3 comandos.
 2. Comandos administrativos rodam no self-chat.
 3. Apenas mensagens IsFromMe podem virar comandos.
 4. Mensagens criadas pelo próprio bot nunca viram comandos.
 5. Só existe 1 envio ativo.
 6. O máximo é 500 destinatários.
 7. Os envios são sequenciais.
 8. Um envio interrompido nunca é retomado automaticamente.
 9. Conteúdo das mensagens e nomes não são persistidos nem logados.
10. internal/whatsapp é a única fronteira com a biblioteca WhatsApp.
11. internal/console é a única fronteira com o terminal.
12. Um único goroutine escreve em stdout.
13. A tela nunca mostra número completo, conteúdo de mensagem, QR em texto,
    chave de sessão ou stack trace.
14. Nenhuma animação bloqueia um envio. Se as duas disputarem, o envio ganha.
```

Os invariantes 10 e 11 são os que se furam sozinhos, e por isso são
**verificáveis por busca**. As duas linhas abaixo têm que sair vazias:

```bash
grep -rl "go.mau.fi/whatsmeow" --include=*.go . | grep -v internal/whatsapp/
grep -rn "fmt.Print\|os.Stdout" --include=*.go . | grep -v internal/console/
```

---

## O que NÃO fazer

Está fora do escopo de propósito, não por falta de tempo:

painel web · API REST · Docker · múltiplas contas · múltiplos usuários ·
templates · CRM · agenda · histórico navegável · `/status` · mídia e anexos ·
confirmação de leitura · métricas · Redis · PostgreSQL · auto-update · plugins ·
proxy de evasão · rotação de contas · "anti-ban" · simulação de digitação ·
retentativa automática · tema configurável · som · ícone na bandeja.

A espera de 1 a 7 segundos é **backpressure operacional**, não técnica
anti-banimento. Não existe taxa documentada que transforme automação não oficial
em uso autorizado, e transformar `dispatch` num motor de heurísticas seria o
começo do fim deste projeto.
