# Abraham

> **Abraham** sends WhatsApp messages to a list of numbers, from your own
> machine. No server, no cloud, no account to create. The interface and commands
> are in Portuguese — it was built for a Brazilian public school.

<p align="center">
  <img src="docs/images/terminal.png" alt="O Abraham rodando no terminal" width="820">
</p>

Dispara mensagens de WhatsApp para uma lista de números, direto do seu
computador. Você conecta o WhatsApp uma vez e comanda tudo pela conversa consigo
mesmo — não tem site, não tem nuvem, não tem conta para criar.

<img src="docs/images/mascote.png" alt="Abraham, o suricato detetive" width="110" align="right">

**O problema.** Escola manda recado para responsável todo dia: reunião, falta,
boletim, evento. Um por um consome a manhã de alguém. Planilha com 300 números e
copiar-colar é pior — erra destinatário.

O Abraham faz isso em um comando, um número de cada vez, e diz quem não recebeu.

---

## Começar

> **Baixe o pacote pronto na aba [Releases](../../releases)** deste
> repositório (`abraham-windows.zip`) — não o código-fonte pelo botão
> "Code". O ZIP do "Code" só tem o Go-fonte, sem `abraham.exe`; quem clicar
> em `instalar.bat` ali vai ver um erro dizendo que faltou o executável.

1. Descompacte o `abraham-windows.zip` onde quiser.
2. Dois cliques em **instalar.bat**.
   > Pode aparecer **"O Windows protegeu o computador"** (SmartScreen) antes
   > de instalar — normal em qualquer programa novo sem assinatura digital
   > paga, não é erro. Clique em **Mais informações → Executar assim mesmo**.
   > Só aparece uma vez; o instalador já remove esse aviso do programa
   > instalado, então os próximos cliques abrem direto.

   Ele copia o programa, cria o atalho na Área de Trabalho e no Menu Iniciar,
   e **já abre o Abraham sozinho** — não precisa fazer mais nada.
3. Na primeira vez aparece um código na tela. No celular: **WhatsApp → menu →
   Aparelhos conectados → Conectar aparelho**, e aponte a câmera.
4. Quando aparecer **Conectado**, deixe de lado e use o WhatsApp normalmente.

Da segunda vez em diante ele lembra a sessão e nem pede o código.

Das próximas vezes: aperte a tecla Windows e digite **"Abraham"** — ele
aparece do mesmo jeito que qualquer programa instalado. Clique direito no
atalho e **Fixar na barra de tarefas** para abrir com um clique só. Ou use o
atalho que ficou na Área de Trabalho.

> `instalar.bat` também deixa o comando `abraham` disponível em qualquer
> terminal, para quem prefere assim. **desinstalar.bat** desfaz tudo, e não
> apaga seus dados sem avisar. Quem preferir não instalar nada pode abrir
> **abraham.exe** direto — é o programa inteiro, sozinho.

> **Se o atalho parar de abrir do nada** ("Atalho não encontrado, procurando
> abraham.exe"): o Abraham não tem assinatura digital — um certificado custa
> e não faz sentido para um programa pequeno de escola — e o Windows Defender
> às vezes apaga um executável assim, mesmo sem ele fazer nada de errado.
> Não é o instalador se auto-elevando para mexer no antivírus por trás: em
> **Segurança do Windows → Proteção contra vírus e ameaças → Histórico de
> proteção**, restaure o Abraham se aparecer lá, e adicione a pasta como
> exceção nas configurações de proteção em tempo real. Depois rode
> `instalar.bat` de novo.

---

## Os três comandos

Vão **na conversa do WhatsApp com você mesmo**. É a única que o Abraham escuta.

### `/enviar`

```
/enviar
5511999999999 Maria
5511888888888

Olá {nome}, a reunião de pais foi adiada para sexta às 19h.
```

- primeira linha: `/enviar`
- os números, um por linha, com o nome depois se quiser
- **uma linha em branco**
- o resto é a mensagem

O `{nome}` vira o nome daquela pessoa. Quem não tem nome recebe a frase limpa: o
marcador some junto com o espaço antes dele, sem deixar `"Olá ,"`.

Os números aceitam `+`, parênteses, traço e espaço. O que ele **não** adivinha é
código de país e DDD — se faltar, o número vai errado.

**Mensagens diferentes no mesmo comando:** repita o `/enviar`.

```
/enviar
5511999999999 Maria

Reunião de pais na sexta às 19h.

/enviar
5511888888888 João

O boletim já está disponível no portal.
```

Continua sendo **um** envio: um `/cancelar` para tudo, um `/falhas` cobre tudo, e
o limite de 500 é o total.

### `/cancelar`

Para na hora, sem perguntar. O que já saiu continua enviado — não existe
"desenviar". Quem faltava aparece no `/falhas`.

### `/falhas`

Lista quem não recebeu. Se houver envio rodando, mostra as falhas dele até
agora; senão, as do último.

### Quanto tempo leva

O Abraham espera **de 1 a 7 segundos** entre uma mensagem e a próxima, sorteado.

| destinatários | mais ou menos |
|---:|---|
| 10 | menos de 1 minuto |
| 50 | 3 minutos |
| 500 | 33 minutos |

Ele avisa a previsão quando começa. Deixe a janela aberta até terminar.

---

## Risco de a conta ser restrita

**Leia antes de usar.** O WhatsApp não foi feito para disparo automático e os
termos restringem esse comportamento. O Abraham fala com ele por um cliente não
oficial. **A conta pode ser limitada ou bloqueada** — não existe ajuste,
intervalo ou volume que torne o uso autorizado.

**O que derruba uma conta não é velocidade.** É bloqueio e denúncia de quem
recebe: cem mensagens esperadas passam batido, vinte indesejadas viram restrição.
Desde 2026 entrou mais um sinal — mensagem que ninguém responde.

O que o Abraham faz: um envio por vez, espera sorteada de 1 a 7 s, sem
retentativa, `{nome}` para quebrar o texto idêntico, e **parada imediata** se o
WhatsApp restringir a conta.

O que ele não faz: simular digitação, aquecer conta, rotacionar número ou proxy.

**Proxy e VPN não protegem contra restrição de conta.** A punição mira o
*número*, não o IP. Existe uma opção de proxy aqui, mas é para **conectividade**
— rede que bloqueia as portas do WhatsApp:

```
ABRAHAM_PROXY=socks5://usuario:senha@servidor:1080
```

**O que reduz risco de verdade:** mandar só para quem espera receber; usar um
número separado do que a instituição usa para atender, porque uma restrição
derruba os dois juntos; começar devagar em número novo; e respeitar quem pede
para sair.

Para volume grande e recorrente, o caminho sem risco de termos de uso é a **API
oficial do WhatsApp Business**. Este programa não substitui isso.

---

## Onde ficam os dados

```
dados/   a sessão do WhatsApp e o resultado do último envio
logs/    um arquivo por dia, apagados sozinhos depois de 30 dias
```

> **`dados/abraham.db` vale como senha.** Quem tiver esse arquivo consegue usar o
> seu WhatsApp. Não mande por e-mail nem coloque em repositório.

Os logs podem ser compartilhados: telefones aparecem mascarados
(`5511*****4321`) e o conteúdo das mensagens nunca é gravado.

**Se o computador desligar no meio de um envio**, o Abraham avisa quantas foram
e **não reenvia nada sozinho**: as que estavam em trânsito têm resultado
incerto, e reenviar por via das dúvidas mandaria a mensagem duas vezes para uma
pessoa de verdade. Use `/falhas` e decida.

---

## Compilar

Precisa só do [Go](https://go.dev/dl/). Nada de compilador C, Node ou Docker.

```powershell
.\build.ps1
```

Roda `go vet`, `gofmt` e os testes antes de compilar, e gera `dist\abraham.exe`.

```
cmd/abraham        composição e ciclo de vida
internal/command   o que a pessoa escreve  →  o que o bot faz
internal/dispatch  o único envio ativo
internal/store     SQLite
internal/whatsapp  ADAPTADOR — única fronteira com o whatsmeow
internal/console   ADAPTADOR — única fronteira com o terminal
```

Duas fronteiras, e as duas são verificáveis por uma linha de `grep` — as
buscas estão em [ARCHITECTURE.md](ARCHITECTURE.md), que explica o desenho em
dez minutos.

---

## Licença

MIT — veja [LICENSE](LICENSE). Usa
[whatsmeow](https://github.com/tulir/whatsmeow) (MPL-2.0), que é apenas ligado,
não copiado.
