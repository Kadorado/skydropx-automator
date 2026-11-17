package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/customers"
	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/telegram"
)

func TestCustomerListFromOrders(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	store := customers.NewMemoryStore()
	b.opts.Customers = store

	data := domain.ShipmentData{Nombre: "Ana Pérez", Documento: "1.111.111", Telefono: "300 111 2233", Ciudad: "Montería",
		Departamento: "Córdoba", Cantidad: 2, ValorUnitario: 60000, AutorizaDatos: "si", Fuente: "facebook"}
	b.recordCustomer(ctx, data, "orden-1", "Vendedor")
	b.recordCustomer(ctx, domain.ShipmentData{Nombre: "Luis", Telefono: "3105556677", Ciudad: "Sincelejo"}, "orden-2", "Vendedor")

	ana, err := store.Find(ctx, "1111111")
	if err != nil || !ana.Contactable() || ana.Consent.RecordedBy != "Vendedor" || len(ana.Orders) != 1 ||
		ana.Orders[0].Units != 2 || ana.Orders[0].UnitPrice != 60000 || ana.Source != "facebook" {
		t.Fatalf("Ana: %+v %v", ana, err)
	}

	b.customersCommand(ctx, -100, "/clientes")
	resumen := f.messages[len(f.messages)-1]
	for _, want := range []string{"BASE DE CLIENTES", "Clientes: <b>2</b>", "Autorizaron publicidad: <b>1</b>", "Sin autorización: 1", "Montería: 1", "facebook: 1", "Sin indicar: 1"} {
		if !strings.Contains(resumen, want) {
			t.Errorf("falta %q en:\n%s", want, resumen)
		}
	}
	if !strings.Contains(string(f.document), "Ana Pérez,1111111,3001112233") || !strings.Contains(f.caption, "Base de clientes") {
		t.Fatalf("debe enviar el CSV, llegó %q (%s)", f.document, f.caption)
	}

	b.consentCommand(ctx, -100, "Tú", "/autoriza 3105556677 respondió SÍ por WhatsApp")
	if luis, _ := store.Find(ctx, "3105556677"); !luis.Contactable() || luis.Consent.Evidence != "respondió SÍ por WhatsApp" {
		t.Fatalf("autoriza: %+v", luis)
	}
	b.optOutCommand(ctx, -100, "/baja 3001112233")
	if ana, err := store.Find(ctx, "3001112233"); err != nil || ana.OptOutAt == nil || ana.Contactable() {
		t.Fatal("después de /baja no debe poder recibir publicidad")
	}
	b.optOutCommand(ctx, -100, "/baja 3209999999")
	if last := f.messages[len(f.messages)-1]; !strings.Contains(last, "No encontré ese cliente") {
		t.Errorf("cliente desconocido: %s", last)
	}
}

func TestCustomerCommandsWithoutMongo(t *testing.T) {
	b, f := newTestBot(t)
	b.customersCommand(context.Background(), -100, "/clientes")
	if last := f.messages[len(f.messages)-1]; !strings.Contains(last, "MONGODB_URI") {
		t.Errorf("sin base debe decir qué falta: %s", last)
	}
}

func TestMigrationText(t *testing.T) {
	since := time.Date(2026, 11, 16, 18, 45, 0, 0, colombiaTZ)
	msg := migrationText(customers.MigrationResult{Shipments: 120, New: 80, OrdersAdded: 110, AfterSince: 4, NoKeys: 6}, since, false)
	for _, want := range []string{"Envíos leídos: 120", "Clientes nuevos: <b>80</b>", "Pedidos agregados: 110",
		"lun 16 nov 6:45 p. m. en adelante (ya los guardó el bot): 4", "Sin cédula ni teléfono (no se guardaron): 6", "skydropx"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "Ya estaban migrados") || strings.Contains(msg, "más antiguos") {
		t.Errorf("no debe mostrar líneas en cero:\n%s", msg)
	}
}

func TestUnsavedCustomerIsReportedAndRetried(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	b.opts.GestionChatIDs = []int64{-100}
	// MONGODB_URI was set but MongoDB didn't connect at startup.
	b.opts.CustomersError = "server selection error: mongodb+srv://user:secret@cluster0.x.mongodb.net tls: internal error"

	b.customersAtStartup(ctx)
	if last := f.messages[len(f.messages)-1]; !strings.Contains(last, "La base de clientes no se conectó") ||
		!strings.Contains(last, "Qué hacer") || strings.Contains(last, "secret") {
		t.Fatalf("startup notice (without the password): %s", last)
	}

	data := domain.ShipmentData{Nombre: "Ana Pérez", Telefono: "3001112233", Ciudad: "Montería", Cantidad: 1, AutorizaDatos: "si"}
	b.recordCustomer(ctx, data, "orden-1", "Vendedor")
	notice := f.messages[len(f.messages)-1]
	for _, want := range []string{"No se guardó el cliente", "Ana Pérez", "no se conectó cuando arrancó", "Reintentar", "no se pierde"} {
		if !strings.Contains(notice, want) {
			t.Errorf("falta %q en:\n%s", want, notice)
		}
	}
	if strings.Contains(notice, "secret") {
		t.Fatal("the notice must never show the connection string")
	}
	if u, ok := b.st.getUnsaved("orden-1"); !ok || u.Sender != "Vendedor" {
		t.Fatal("the customer must be kept aside")
	}

	// Still not connected: the retry says so and keeps the button.
	b.retryCustomer(ctx, "cb", -100, 1, "orden-1")
	if last := f.edits[len(f.edits)-1]; !strings.Contains(last, "Último intento") {
		t.Fatalf("failed retry: %s", last)
	}

	// MongoDB connects: the retry saves it with the original order date and consent.
	store := customers.NewMemoryStore()
	b.opts.Customers, b.opts.CustomersError = store, ""
	b.retryCustomer(ctx, "cb", -100, 1, "orden-1")
	if last := f.edits[len(f.edits)-1]; !strings.Contains(last, "quedó guardado") {
		t.Fatalf("successful retry: %s", last)
	}
	ana, err := store.Find(ctx, "3001112233")
	if err != nil || !ana.Contactable() || len(ana.Orders) != 1 {
		t.Fatalf("saved customer: %+v %v", ana, err)
	}
	if _, ok := b.st.getUnsaved("orden-1"); ok {
		t.Fatal("a saved customer must leave the queue")
	}
	b.retryCustomer(ctx, "cb", -100, 1, "orden-1")
	if last := f.edits[len(f.edits)-1]; !strings.Contains(last, "Cliente guardado") {
		t.Fatalf("second tap: %s", last)
	}
}

func TestUnsavedCustomersAreSavedAtStartup(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	b.opts.GestionChatIDs = []int64{-100}
	for _, id := range []string{"o1", "o2"} {
		_ = b.st.addUnsaved(UnsavedCustomer{OrderID: id, Data: domain.ShipmentData{Nombre: id, Telefono: "300000000" + id[1:]}, At: time.Now()})
	}
	b.opts.Customers = customers.NewMemoryStore()
	b.customersAtStartup(ctx)
	if last := f.messages[len(f.messages)-1]; !strings.Contains(last, "<b>2</b> clientes que habían quedado apartados") {
		t.Fatalf("startup flush: %s", last)
	}
	if len(b.st.snapshotUnsaved()) != 0 {
		t.Fatal("the queue must be empty")
	}
	n := len(f.messages)
	b.customersAtStartup(ctx)
	if len(f.messages) != n {
		t.Fatal("nothing to report when nothing was pending")
	}
}

func TestCustomerListNotConfiguredIsSilent(t *testing.T) {
	b, f := newTestBot(t)
	b.opts.GestionChatIDs = []int64{-100}
	b.customersAtStartup(context.Background())
	b.recordCustomer(context.Background(), domain.ShipmentData{Nombre: "Ana", Telefono: "3001112233"}, "o1", "V")
	if len(f.messages) != 0 || len(b.st.snapshotUnsaved()) != 0 {
		t.Fatalf("without MONGODB_URI nothing is said nor kept: %v", f.messages)
	}
}

func TestBackupAndRestore(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	b.opts.GestionChatIDs = []int64{-100}
	store := customers.NewMemoryStore()
	b.opts.Customers = store
	b.recordCustomer(ctx, domain.ShipmentData{Nombre: "Ana Pérez", Documento: "111111", Telefono: "3001112233", Cantidad: 2, AutorizaDatos: "si"}, "o1", "V")
	b.recordCustomer(ctx, domain.ShipmentData{Nombre: "Luis", Telefono: "3105556677"}, "o2", "V")

	b.customersCommand(ctx, -100, "/clientes respaldo")
	if !strings.Contains(f.caption, "2 clientes · 📦 2 pedidos") || !strings.Contains(f.caption, "/clientes restaurar") {
		t.Fatalf("caption: %s", f.caption)
	}
	backupFile := append([]byte(nil), f.document...)

	// The list is lost: a new, empty database. Restore by replying to the backup file.
	b.opts.Customers = customers.NewMemoryStore()
	f.fileData = backupFile
	reply := &telegram.Message{Chat: telegram.Chat{ID: -100}, Text: "/clientes restaurar",
		ReplyTo: &telegram.Message{Document: &telegram.Document{FileID: "F1", FileName: "clientes-respaldo.json", FileSize: int64(len(backupFile))}}}
	if !isRestoreCommand(reply) {
		t.Fatal("a reply to a file with /clientes restaurar is a restore")
	}
	b.restoreCommand(ctx, reply)
	if last := f.messages[len(f.messages)-1]; !strings.Contains(last, "RESTAURACIÓN TERMINADA") || !strings.Contains(last, "Recuperados: <b>2</b>") {
		t.Fatalf("restore answer: %s", last)
	}
	ana, err := b.opts.Customers.Find(ctx, "111111")
	if err != nil || !ana.Contactable() || ana.Orders[0].Units != 2 {
		t.Fatalf("restored: %+v %v", ana, err)
	}

	// Not a backup (e.g. the Excel): rejected without touching anything.
	f.fileData = []byte("nombre,cedula\nAna,111111")
	b.restoreCommand(ctx, reply)
	if last := f.messages[len(f.messages)-1]; !strings.Contains(last, "no es un respaldo de clientes") {
		t.Fatalf("wrong file: %s", last)
	}
	// Outside the gestión group it does nothing.
	other := *reply
	other.Chat.ID = -200
	b.restoreCommand(ctx, &other)
	if last := f.messages[len(f.messages)-1]; !strings.Contains(last, "solo funciona en el grupo de gestión") {
		t.Fatalf("other chat: %s", last)
	}
}

func TestIsRestoreCommand(t *testing.T) {
	doc := &telegram.Document{FileID: "F"}
	cases := []struct {
		m    telegram.Message
		want bool
	}{
		{telegram.Message{Caption: "/clientes restaurar", Document: doc}, true},
		{telegram.Message{Text: "/clientes restaurar", ReplyTo: &telegram.Message{Document: doc}}, true},
		{telegram.Message{Text: "/clientes@MiBot restaurar", ReplyTo: &telegram.Message{Document: doc}}, true},
		{telegram.Message{Text: "/clientes restaurar"}, false},                                           // no file: instructions
		{telegram.Message{Text: "/clientes", ReplyTo: &telegram.Message{Document: doc}}, false},          // another command
		{telegram.Message{Text: "/clientes restaurar", ReplyTo: &telegram.Message{Text: "hola"}}, false}, // reply without file
	}
	for i, c := range cases {
		if got := isRestoreCommand(&c.m); got != c.want {
			t.Errorf("case %d: %v", i, got)
		}
	}
}

func TestWeeklyBackup(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	b.opts.GestionChatIDs = []int64{-100}
	b.opts.Customers = customers.NewMemoryStore()
	monday := time.Date(2026, 11, 16, 6, 0, 0, 0, colombiaTZ)
	b.backupIfDue(ctx, monday)                                   // too early
	b.backupIfDue(ctx, monday.AddDate(0, 0, 1).Add(3*time.Hour)) // Tuesday
	if f.document != nil {
		t.Fatal("only Mondays from 7 a. m.")
	}
	b.backupIfDue(ctx, monday.Add(2*time.Hour))
	if !strings.Contains(f.caption, "Respaldo de la base de clientes") {
		t.Fatalf("monday backup: %q", f.caption)
	}
	f.caption = ""
	b.backupIfDue(ctx, monday.Add(5*time.Hour))
	if f.caption != "" {
		t.Fatal("once per Monday")
	}
}

func TestHelpTextsFitTelegram(t *testing.T) {
	for name, txt := range map[string]string{"gestion": ayudaGestion, "vendedor": ayudaVendedor, "guias": ayudaGrupoGuias} {
		if n := len([]rune(txt)); n > 4096 {
			t.Errorf("%s: %d characters, Telegram allows 4096", name, n)
		}
		for _, tag := range []string{"b", "i", "code", "blockquote"} {
			open := strings.Count(txt, "<"+tag+">") + strings.Count(txt, "<"+tag+" ")
			if closed := strings.Count(txt, "</"+tag+">"); open != closed {
				t.Errorf("%s: <%s> opened %d times, closed %d", name, tag, open, closed)
			}
		}
	}
}
