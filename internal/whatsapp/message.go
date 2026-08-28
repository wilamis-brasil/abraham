package whatsapp

import (
	"log"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/wilamis-brasil/abraham/internal/command"
)

// handle turns whatsmeow's events into the few the bot understands. Anything
// not listed here is ignored on purpose.
func (c *Client) handle(raw any) {
	switch e := raw.(type) {
	case *events.Message:
		c.handleMessage(e)
	case *events.Connected, *events.PushNameSetting:
		c.emit(Event{Kind: Connected})
	case *events.Disconnected:
		c.emit(Event{Kind: Disconnected})
	case *events.LoggedOut:
		c.emit(Event{Kind: LoggedOut, Text: e.Reason.String()})
	case *events.StreamReplaced:
		// Another connection took over the same keys. Insisting would mean
		// fighting for a session that is no longer ours.
		c.emit(Event{Kind: LoggedOut, Text: "a sessão foi aberta em outro lugar"})
	case *events.TemporaryBan:
		c.emit(Event{
			Kind:  Banned,
			Text:  e.Code.String(),
			Until: time.Now().Add(e.Expire),
		})
	}
}

func (c *Client) handleMessage(e *events.Message) {
	// Someone else's message does not even reach the log: that would be hundreds
	// of lines a day with no use.
	if !e.Info.IsFromMe {
		return
	}

	// Self-chat only. A /enviar typed into a parent's conversation exposes the
	// other recipients' numbers to them before the bot even reads the command —
	// and deleting it afterwards does not help, the notification already went
	// out.
	if !isSelfChat(e.Info.MessageSource, c.identities()) {
		c.ignored("outra-conversa", e.Info.MessageSource)
		return
	}

	// From here on, this conversation is the control channel. Remember the exact
	// address so replies go back the way they came.
	c.rememberChat(e.Info.Chat)

	// It must not be a message this very process just sent, or a send whose text
	// starts with "/enviar" would fire itself, in a loop.
	if c.wasMine(e.Info.ID) {
		c.ignored("eco-do-proprio-bot", e.Info.MessageSource)
		return
	}

	text := plainText(e.Message)
	if strings.TrimSpace(text) == "" {
		c.ignored("sem-texto", e.Info.MessageSource)
		return
	}

	cmd, err := command.Parse(text)
	if err != nil {
		log.Printf("INFO  comando recusado motivo=formato")
		c.emit(Event{Kind: CommandError, Text: err.Error()})
		return
	}
	if cmd.Kind == command.None {
		c.ignored("nao-e-comando", e.Info.MessageSource)
		return
	}

	if cmd.Kind == command.Send {
		log.Printf("INFO  comando recebido tipo=enviar total=%d blocos=%d",
			len(cmd.Targets), cmd.Blocks)
	} else {
		log.Printf("INFO  comando recebido tipo=%s", commandName(cmd.Kind))
	}
	c.emit(Event{Kind: CommandKind, Cmd: cmd})
}

// ignored records why one of OUR messages did not become a command.
//
// No number and no content (invariant 13): just the reason and the address
// type. It is the line that answers on its own the next time the bot goes
// mute — its absence once cost two rounds of guessing.
func (c *Client) ignored(reason string, src types.MessageSource) {
	log.Printf("INFO  mensagem ignorada motivo=%s conversa=%s remetente=%s",
		reason, src.Chat.Server, src.Sender.Server)
}

func commandName(k command.Kind) string {
	switch k {
	case command.Send:
		return "enviar"
	case command.Cancel:
		return "cancelar"
	case command.Failures:
		return "falhas"
	default:
		return "?"
	}
}

// identities returns the account's known addresses, for the fallback below.
func (c *Client) identities() []types.JID {
	var ids []types.JID
	if c.wa.Store.ID != nil {
		ids = append(ids, *c.wa.Store.ID)
	}
	if lid := c.wa.Store.GetLID(); !lid.IsEmpty() {
		ids = append(ids, lid)
	}
	return ids
}

// isSelfChat decides whether the message sits in the account's conversation
// with itself.
//
// The main rule does not need to know who I am: on any message of mine,
// whatsmeow fills Sender with who sent it (me) and Chat with who it went to. If
// those are the same person, the conversation is with myself. Both fields come
// from the same event, in the same addressing mode, always consistent with each
// other.
//
// The previous version compared Chat against the account's stored identity and
// broke twice: WhatsApp is migrating from phone numbers (@s.whatsapp.net) to LID
// (@lid), and guessing which one is in play is a bet that does not need making.
//
// The known identities stay as a FALLBACK, for the protocol paths that leave
// Sender empty.
func isSelfChat(src types.MessageSource, identities []types.JID) bool {
	if !src.IsFromMe || src.IsGroup {
		return false
	}
	chat := src.Chat.ToNonAD()
	if chat.User == "" {
		return false
	}

	if sender := src.Sender.ToNonAD(); sender.User != "" {
		return chat.User == sender.User
	}

	for _, id := range identities {
		if !id.IsEmpty() && chat.User == id.ToNonAD().User {
			return true
		}
	}
	return false
}

// plainText pulls out the simple text. A message with media, a poll, a contact
// or anything else returns "" and is ignored — this program only understands
// text.
func plainText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if s := m.GetConversation(); s != "" {
		return s
	}
	return m.GetExtendedTextMessage().GetText()
}
