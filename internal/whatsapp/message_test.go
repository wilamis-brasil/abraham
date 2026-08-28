package whatsapp

import (
	"testing"

	"github.com/wilamis-brasil/abraham/internal/command"

	"go.mau.fi/whatsmeow/types"
)

// As duas identidades desta conta, como estao no banco de verdade.
var (
	myPhone = types.JID{User: "551146679001", Server: types.DefaultUserServer}
	myLID   = types.JID{User: "170768604348644", Server: types.HiddenUserServer}
	myIDs   = []types.JID{myPhone, myLID}
)

// jid monta um JID com sufixo de dispositivo, que e como o protocolo entrega.
func jid(base types.JID, dispositivo uint16) types.JID {
	base.Device = dispositivo
	return base
}

func TestDeteccaoDaConversaComigo(t *testing.T) {
	outraPessoa := types.JID{User: "5511900000000", Server: types.DefaultUserServer}
	outraPessoaLID := types.JID{User: "999888777666555", Server: types.HiddenUserServer}
	grupo := types.JID{User: "120363000000000000", Server: types.GroupServer}

	casos := []struct {
		nome string
		src  types.MessageSource
		quer bool
	}{
		{
			// O caminho antigo: conta ainda nao migrada.
			nome: "self-chat por telefone",
			src:  types.MessageSource{IsFromMe: true, Chat: myPhone, Sender: jid(myPhone, 29)},
			quer: true,
		},
		{
			// ESTE e o caso que quebrou de verdade. O log registrou
			// "servidor=lid" tres vezes e o bot ficou mudo.
			nome: "self-chat por LID",
			src:  types.MessageSource{IsFromMe: true, Chat: myLID, Sender: jid(myLID, 29)},
			quer: true,
		},
		{
			// A regra nao pode depender de o LID bater com o que esta guardado:
			// aqui o Chat vem com um Integrator diferente, que o ToNonAD()
			// preserva. Comparar contra a identidade guardada falharia; comparar
			// Chat com Sender, nao.
			nome: "self-chat por LID com Integrator diferente",
			src: types.MessageSource{
				IsFromMe: true,
				Chat:     types.JID{User: myLID.User, Server: types.HiddenUserServer, Integrator: 7},
				Sender:   types.JID{User: myLID.User, Server: types.HiddenUserServer, Device: 29},
			},
			quer: true,
		},
		{
			// E o caso que mais importa barrar: um /enviar escrito na conversa
			// de outra pessoa expoe os numeros dos destinatarios para ela.
			nome: "mensagem minha para outra pessoa",
			src:  types.MessageSource{IsFromMe: true, Chat: outraPessoa, Sender: jid(myPhone, 29)},
			quer: false,
		},
		{
			nome: "mensagem minha para outra pessoa, por LID",
			src:  types.MessageSource{IsFromMe: true, Chat: outraPessoaLID, Sender: jid(myLID, 29)},
			quer: false,
		},
		{
			nome: "mensagem minha num grupo",
			src:  types.MessageSource{IsFromMe: true, IsGroup: true, Chat: grupo, Sender: jid(myPhone, 29)},
			quer: false,
		},
		{
			nome: "mensagem de outra pessoa",
			src:  types.MessageSource{IsFromMe: false, Chat: outraPessoa, Sender: outraPessoa},
			quer: false,
		},
		{
			// Segunda via: alguns caminhos do protocolo nao preenchem o Sender.
			// Ai voltamos a comparar com as identidades conhecidas.
			nome: "sender vazio, chat e a minha conta",
			src:  types.MessageSource{IsFromMe: true, Chat: myLID},
			quer: true,
		},
		{
			nome: "sender vazio, chat e de outra pessoa",
			src:  types.MessageSource{IsFromMe: true, Chat: outraPessoa},
			quer: false,
		},
		{
			nome: "tudo vazio",
			src:  types.MessageSource{IsFromMe: true},
			quer: false,
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := isSelfChat(c.src, myIDs); got != c.quer {
				t.Errorf("isSelfChat = %v, queria %v\n  chat=%s sender=%s",
					got, c.quer, c.src.Chat, c.src.Sender)
			}
		})
	}
}

func TestDeteccaoNaoPrecisaDasIdentidades(t *testing.T) {
	// O ponto da correcao: a regra principal funciona mesmo sem saber quem a
	// conta e. Se ela precisasse da identidade guardada, voltaria a quebrar toda
	// vez que o WhatsApp mudasse o modo de enderecamento.
	src := types.MessageSource{
		IsFromMe: true,
		Chat:     types.JID{User: "identidade-nunca-vista", Server: types.HiddenUserServer},
		Sender:   types.JID{User: "identidade-nunca-vista", Server: types.HiddenUserServer, Device: 3},
	}
	if !isSelfChat(src, nil) {
		t.Error("com Chat e Sender iguais, a conversa é comigo mesmo sem lista de identidades")
	}
}

func TestNomeComando(t *testing.T) {
	// O nome vai para o log, que é a ferramenta de diagnóstico. Um rótulo errado
	// aqui manda a próxima investigação para o lugar errado.
	for tipo, quer := range map[command.Kind]string{
		command.Send:     "enviar",
		command.Cancel:   "cancelar",
		command.Failures: "falhas",
		command.None:     "?",
	} {
		if got := commandName(tipo); got != quer {
			t.Errorf("commandName(%v) = %q, queria %q", tipo, got, quer)
		}
	}
}
