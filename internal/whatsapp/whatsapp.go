// Package whatsapp is the ONLY boundary with the whatsmeow library.
// (invariant 10)
//
// No other package imports it. That exists for a very concrete reason: WhatsApp
// changes its protocol from time to time and one day this will break. When it
// does, the investigation starts and ends in this package — the rest of the bot
// does not even need to be read.
//
// To the rest of the program, WhatsApp is:
//
//	somewhere to send text  ->  Send / Reply
//	a channel of things that happened  ->  Event
//
// That is all. It is what lets the whole dispatcher be tested without an
// account.
package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/wilamis-brasil/abraham/internal/command"
)

// EventKind is the small set of things the bot cares about.
type EventKind int

const (
	QRCode       EventKind = iota // a fresh pairing code to draw
	Paired                        // the phone read the code
	Connected                     // ready to send
	Disconnected                  // temporary drop; whatsmeow reconnects on its own
	LoggedOut                     // the session died for good; back to the QR
	Banned                        // temporary restriction on the account
	CommandKind                   // one of the three commands arrived via self-chat
	CommandError                  // a malformed /enviar; Text goes to the user
	Confirmation                  // WhatsApp wants a code checked; Text is the code
)

// Event is everything the bot needs to know about the outside world.
type Event struct {
	Kind  EventKind
	QR    string          // QRCode
	Cmd   command.Command // CommandKind
	Text  string          // CommandError, Banned, LoggedOut
	Until time.Time       // Banned: when the restriction expires
}

// OpenSession loads the already-paired device from the database, or prepares a
// new one.
//
// It lives here, and not in main, because sqlstore is whatsmeow and main must
// not know it. (invariant 10) It reuses the SAME SQLite connection as the store
// package, so whatsmeow's tables and ours live in one file.
func OpenSession(ctx context.Context, db *sql.DB, dialect string) (*store.Device, error) {
	container := sqlstore.NewWithDB(db, dialect, waLog.Noop)
	// NewWithDB does not run the migrations by itself, unlike New.
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("could not prepare the WhatsApp session: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not read the WhatsApp session: %w", err)
	}
	if device == nil {
		device = container.NewDevice() // first run: it will ask for the QR
	}
	return device, nil
}

// maxRememberedIDs caps the ring of message IDs this process created.
const maxRememberedIDs = 200

// Client is the real implementation, on top of whatsmeow.
type Client struct {
	wa     *whatsmeow.Client
	events chan Event

	// ownIDs holds the IDs of messages THIS process created.
	//
	// Every message the bot sends is also "IsFromMe" and comes back as an event.
	// Without this filter, a message whose text starts with "/enviar" would be
	// re-read as a command and fire itself. It also stops the bot's own
	// administrative replies from looping.
	muIDs   sync.Mutex
	ownIDs  map[types.MessageID]bool
	idOrder []types.MessageID

	// chat is the EXACT address the last command arrived from.
	//
	// Replies go back through it, not through a JID rebuilt from the account:
	// WhatsApp is migrating from phone numbers (@s.whatsapp.net) to LID (@lid),
	// and rebuilding the address means betting on which of the two is in play.
	// When that bet loses, the bot goes mute without raising an error.
	muChat sync.Mutex
	chat   types.JID
}

// New builds the client on top of a device already loaded from the database.
func New(device *store.Device) *Client {
	c := &Client{
		wa:     whatsmeow.NewClient(device, waLog.Noop),
		events: make(chan Event, 32),
		ownIDs: make(map[types.MessageID]bool, maxRememberedIDs),
	}
	c.wa.AddEventHandler(c.handle)
	return c
}

// Events is closed when the client shuts down.
func (c *Client) Events() <-chan Event { return c.events }

// UseProxy routes the connection through an HTTP or SOCKS5 proxy.
//
// This is for CONNECTIVITY and nothing else: a corporate network that blocks
// WhatsApp's ports, or an internet link that requires a proxy. It does NOT
// protect against account restriction — WhatsApp's punishment targets the
// number, not the IP, and changing IP changes nothing about that. A shared
// proxy usually makes things worse: an IP with a bad history contaminates
// whoever lands on it.
//
// An empty address connects directly, which is the default.
func (c *Client) UseProxy(address string) error {
	if address == "" {
		return nil
	}
	return c.wa.SetProxyAddress(address)
}

// Connect brings the client up. With no session yet, it emits QRCode events
// until the phone reads one.
func (c *Client) Connect(ctx context.Context) error {
	if c.wa.Store.ID != nil {
		return c.wa.Connect()
	}

	// GetQRChannel has to come BEFORE Connect, otherwise the first codes are
	// lost and the pairing screen sits blank for twenty seconds.
	qr, err := c.wa.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("could not start pairing: %w", err)
	}
	if err := c.wa.Connect(); err != nil {
		return err
	}
	go c.pumpQR(qr)
	return nil
}

func (c *Client) pumpQR(qr <-chan whatsmeow.QRChannelItem) {
	for item := range qr {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			c.emit(Event{Kind: QRCode, QR: item.Code})

		case "success":
			c.emit(Event{Kind: Paired})

		case whatsmeow.QRChannelEventPasskeyResponse:
			// After reading the code, WhatsApp shows a number on the phone and
			// expects the computer to show the same one. It is a visual check
			// against someone in the middle.
			//
			// We confirm automatically: the console has no keyboard, and whoever
			// scanned the QR is the same person, at this machine, seconds ago.
			// The code still appears on screen for them to compare.
			code := ""
			if item.PasskeyConfirmation != nil {
				code = item.PasskeyConfirmation.Code
			}
			c.emit(Event{Kind: Confirmation, Text: code})
			if err := c.wa.SendPasskeyConfirmation(context.Background()); err != nil {
				c.emit(Event{Kind: LoggedOut,
					Text: "a confirmação do código falhou: " + err.Error()})
			}

		case whatsmeow.QRChannelEventPasskeyRequest:
			// This path wants a real WebAuthn credential, which only a password
			// manager or the operating system can sign. A console bot cannot.
			// Saying so plainly beats leaving the screen stuck with no reason.
			c.emit(Event{Kind: LoggedOut,
				Text: "esta conta pede conexão por chave de acesso, que o Abraham não suporta"})

		default:
			// timeout, pairing error, client disconnected. The event name goes
			// into the text: without it, any protocol novelty turns into a mute
			// "session ended" that is impossible to diagnose.
			text := item.Event
			if item.Error != nil {
				text = item.Event + ": " + item.Error.Error()
			}
			c.emit(Event{Kind: LoggedOut, Text: text})
		}
	}
}

// Close disconnects and closes the event channel.
func (c *Client) Close() {
	c.wa.Disconnect()
	close(c.events)
}

// Unpair deletes the session on the server. Only --desconectar calls this.
func (c *Client) Unpair(ctx context.Context) error {
	return c.wa.Logout(ctx)
}

// Number is the phone number of the connected account, or "" before pairing.
func (c *Client) Number() string {
	if c.wa.Store.ID == nil {
		return ""
	}
	return c.wa.Store.ID.User
}

// Send delivers a message to an already-normalised number. It satisfies
// dispatch.Sender through Go's implicit interfaces.
//
// Success means "the server accepted it", not "the person read it".
func (c *Client) Send(ctx context.Context, number, text string) error {
	return c.sendTo(ctx, types.JID{User: number, Server: types.DefaultUserServer}, text)
}

// Reply writes back into the conversation the last command came from.
func (c *Client) Reply(ctx context.Context, text string) error {
	target := c.currentChat()
	if target.IsEmpty() {
		// No command has arrived in this session yet: fall back to the account.
		// Only happens if something tries to reply before there is anyone to
		// reply to.
		if c.wa.Store.ID == nil {
			return fmt.Errorf("no active session")
		}
		target = c.wa.Store.ID.ToNonAD()
	}
	return c.sendTo(ctx, target, text)
}

func (c *Client) sendTo(ctx context.Context, target types.JID, text string) error {
	resp, err := c.wa.SendMessage(ctx, target, &waE2E.Message{
		Conversation: proto.String(text),
	})
	if err != nil {
		return err
	}
	c.rememberID(resp.ID)
	return nil
}

func (c *Client) rememberChat(jid types.JID) {
	c.muChat.Lock()
	c.chat = jid
	c.muChat.Unlock()
}

func (c *Client) currentChat() types.JID {
	c.muChat.Lock()
	defer c.muChat.Unlock()
	return c.chat
}

func (c *Client) rememberID(id types.MessageID) {
	c.muIDs.Lock()
	defer c.muIDs.Unlock()
	c.ownIDs[id] = true
	c.idOrder = append(c.idOrder, id)
	if len(c.idOrder) > maxRememberedIDs {
		delete(c.ownIDs, c.idOrder[0])
		c.idOrder = c.idOrder[1:]
	}
}

func (c *Client) wasMine(id types.MessageID) bool {
	c.muIDs.Lock()
	defer c.muIDs.Unlock()
	return c.ownIDs[id]
}

func (c *Client) emit(ev Event) {
	// Never blocks: this handler runs inside whatsmeow's event loop, and
	// stalling here would stall the whole connection.
	//
	// But the drop is NOT silent. A command lost without a trace is exactly the
	// kind of failure that makes the bot look broken with no clue why.
	select {
	case c.events <- ev:
	default:
		log.Printf("WARN  evento descartado tipo=%d fila=%d", ev.Kind, len(c.events))
	}
}
