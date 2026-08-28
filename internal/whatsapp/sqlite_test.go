package whatsapp

// sqlite_smoke_test.go — a pergunta que trava a fase 0.
//
// O whatsmeow guarda a sessao num SQLite. O exemplo oficial usa
// mattn/go-sqlite3, que exige CGo e um compilador C para *compilar* — ruim para
// um projeto que precisa ser recompilavel daqui a anos por quem so tem Go
// instalado. modernc.org/sqlite e SQLite traduzido para Go puro: sem CGo, sem
// gcc, binario autocontido.
//
// So que ha um relato upstream de incompatibilidade entre os dois, em torno de
// foreign keys. Este teste existe para responder isso com fato em vez de
// suposicao, e para continuar respondendo: no dia em que uma atualizacao do
// whatsmeow quebrar a stack sem CGo, e aqui que vai aparecer primeiro.
//
// Por que foreign keys sao o ponto sensivel, lendo o fonte do whatsmeow:
//
//   1. Container.Upgrade() consulta `PRAGMA foreign_keys` por uma conexao
//      QUALQUER do pool e aborta com "foreign keys are not enabled" se vier
//      desligado. Ou seja: nao adianta um PRAGMA avulso depois do Open, porque
//      pegaria so numa conexao. Tem que vir na DSN, que o driver aplica em toda
//      conexao nova.
//   2. dbutil.DoSQLiteTransactionWithoutForeignKeys() pega UMA conexao, desliga
//      as foreign keys nela, roda as migrations e religa. Isso depende do driver
//      honrar `PRAGMA foreign_keys=OFF` em tempo de execucao.
//
// A sintaxe da DSN e especifica de cada driver: o mattn usa
// `?_foreign_keys=on`, o modernc usa `?_pragma=foreign_keys(1)`.

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"

	_ "modernc.org/sqlite"
)

// dsnFK e a DSN que o bot vai usar de verdade. O `_pragma` e a forma do
// modernc; ele aplica o pragma em cada conexao aberta pelo pool.
const dsnFK = "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

// dialeto e o que o whatsmeow entende, e nao o nome do driver.
// dbutil.ParseDialect aceita qualquer coisa com prefixo "sqlite", entao aqui os
// dois coincidem — mas sao conceitos diferentes e mudam separadamente.
const dialeto = "sqlite"

func abrir(t *testing.T, caminho string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+caminho+dsnFK)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	return db
}

// TestSQLiteSemCGo prova que modernc.org/sqlite serve de base para o sqlstore do
// whatsmeow: migrations, gravar um device, fechar, reabrir e ler de volta.
func TestSQLiteSemCGo(t *testing.T) {
	ctx := context.Background()
	caminho := filepath.ToSlash(filepath.Join(t.TempDir(), "abraham.db"))

	// ---- primeira abertura: migrations e gravacao ----
	db := abrir(t, caminho)
	defer db.Close()

	// O Upgrade le este pragma por uma conexao do pool. Se falhar aqui, falha
	// la — e a mensagem daqui e muito mais util.
	var fkLigadas bool
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fkLigadas); err != nil {
		t.Fatalf("PRAGMA foreign_keys nao pode ser lido: %v", err)
	}
	if !fkLigadas {
		t.Fatalf("foreign keys vieram desligadas com a DSN %q", dsnFK)
	}

	container := sqlstore.NewWithDB(db, dialeto, nil)

	// NewWithDB nao roda as migrations sozinho, diferente de New.
	if err := container.Upgrade(ctx); err != nil {
		t.Fatalf("migrations do whatsmeow falharam: %v", err)
	}

	device := container.NewDevice()
	jid := types.JID{User: "5511999999999", Server: types.DefaultUserServer}
	device.ID = &jid // PutDevice recusa device sem JID

	// NewDevice devolve Account nil; quem preenche e o pareamento. Como aqui nao
	// ha pareamento, montamos o minimo para o INSERT passar. Os tamanhos nao sao
	// arbitrarios: o esquema do whatsmeow tem CHECK de comprimento exato em
	// quase todo campo binario.
	device.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte("detalhes"),
		AccountSignature:    bytes.Repeat([]byte{1}, 64),
		AccountSignatureKey: bytes.Repeat([]byte{2}, 32),
		DeviceSignature:     bytes.Repeat([]byte{3}, 64),
	}

	if err := container.PutDevice(ctx, device); err != nil {
		t.Fatalf("PutDevice: %v", err)
	}
	registroOriginal := device.RegistrationID

	if err := container.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// ---- segunda abertura: a sessao tem que sobreviver ----
	// E isto que faz o usuario nao precisar escanear o QR toda vez.
	db2 := abrir(t, caminho)
	defer db2.Close()
	container2 := sqlstore.NewWithDB(db2, dialeto, nil)

	if err := container2.Upgrade(ctx); err != nil {
		t.Fatalf("Upgrade na reabertura (deveria ser no-op): %v", err)
	}

	lido, err := container2.GetFirstDevice(ctx)
	if err != nil {
		t.Fatalf("GetFirstDevice: %v", err)
	}
	if lido == nil || lido.ID == nil {
		t.Fatal("device nao voltou da base")
	}
	if lido.ID.User != jid.User {
		t.Errorf("JID lido = %q, queria %q", lido.ID.User, jid.User)
	}
	if lido.RegistrationID != registroOriginal {
		t.Errorf("RegistrationID lido = %d, queria %d", lido.RegistrationID, registroOriginal)
	}
	if len(lido.NoiseKey.Priv) == 0 {
		t.Error("NoiseKey voltou vazia — as chaves da sessao nao persistiram")
	}
}

// TestForeignKeysSaoMesmoAplicadas confere que as foreign keys nao estao apenas
// *reportadas* como ligadas, mas realmente sendo aplicadas. O PRAGMA responder 1
// e uma coisa; o motor recusar a insercao e outra. Sem isto, o teste acima
// passaria mesmo com as chaves inertes.
func TestForeignKeysSaoMesmoAplicadas(t *testing.T) {
	ctx := context.Background()
	caminho := filepath.ToSlash(filepath.Join(t.TempDir(), "fk.db"))

	db := abrir(t, caminho)
	defer db.Close()

	container := sqlstore.NewWithDB(db, dialeto, nil)
	if err := container.Upgrade(ctx); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	// whatsmeow_identity_keys referencia whatsmeow_device(jid) e tem
	// CHECK(length(identity) = 32). Os 32 bytes sao de proposito: com um tamanho
	// errado, quem barraria a insercao seria o CHECK, e o teste passaria sem ter
	// provado nada sobre foreign keys.
	_, err := db.ExecContext(ctx,
		`INSERT INTO whatsmeow_identity_keys (our_jid, their_id, identity)
		 VALUES (?, ?, ?)`,
		"inexistente@s.whatsapp.net", "alguem@s.whatsapp.net",
		bytes.Repeat([]byte{9}, 32))
	if err == nil {
		t.Fatal("insercao com foreign key invalida foi aceita — as chaves estao inertes")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("a insercao falhou, mas nao por foreign key: %v", err)
	}
	t.Logf("foreign key aplicada: %v", err)
}
