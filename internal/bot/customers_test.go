package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/customers"
	"skydropx-automator/internal/domain"
)

func TestCustomerListFromOrders(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	store := customers.NewMemoryStore()
	b.opts.Customers = store

	data := domain.ShipmentData{Nombre: "Ana Pérez", Documento: "1.111.111", Telefono: "300 111 2233", Ciudad: "Montería",
		Departamento: "Córdoba", Cantidad: 2, ValorUnitario: 60000, AutorizaDatos: "si", Fuente: "facebook"}
	b.recordCustomer(ctx, data, "Vendedor")
	b.recordCustomer(ctx, domain.ShipmentData{Nombre: "Luis", Telefono: "3105556677", Ciudad: "Sincelejo"}, "Vendedor")

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
