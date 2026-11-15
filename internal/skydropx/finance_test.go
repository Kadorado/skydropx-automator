package skydropx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"skydropx-automator/internal/config"
)

func TestSummarizeShipmentsComision(t *testing.T) {
	shipments := []financeShipment{
		{OrderID: "o1", OnDelivery: "collected", CODAmount: 72573, Cost: 12573}, // 2 datáfonos
		{OrderID: "o2", OnDelivery: "collected", CODAmount: 72000, Cost: 12000}, // 1 datáfono
		{OrderID: "o3", OnDelivery: "pending", CODAmount: 135000, Cost: 15000},  // 3 datáfonos
		{OrderID: "", OnDelivery: "pending", CODAmount: 70000, Cost: 10000},     // sin orden: se estima 1
		{OrderID: "o5", OnDelivery: "failed", CODAmount: 70000, Cost: 10000},    // no recaudado: sin comisión
		{OrderID: "o6", OnDelivery: "collected", CODAmount: 99999, Cost: 9999, Cancelled: true},
	}
	qty := map[string]int{"o1": 2, "o2": 1, "o3": 3, "o5": 1}
	f := summarizeShipments(shipments, qty, FlatCommission(15000))

	c := f.Commission
	if c.PaidUnits != 3 || c.PaidAmount != 45000 {
		t.Fatalf("cobrados: %d datáfonos / %v, se esperaban 3 / 45000", c.PaidUnits, c.PaidAmount)
	}
	if c.PendingUnits != 4 || c.PendingAmount != 60000 || c.Estimated != 1 {
		t.Fatalf("por cobrar: %d datáfonos / %v (estimados %d), se esperaban 4 / 60000 (1)", c.PendingUnits, c.PendingAmount, c.Estimated)
	}
	if f.Collected != (Money{2, 144573}) || f.Pending != (Money{2, 205000}) || f.Failed != (Money{1, 70000}) {
		t.Fatalf("contra entrega inesperado: cobrado %+v, por cobrar %+v, fallido %+v", f.Collected, f.Pending, f.Failed)
	}
	if f.Shipments != 6 || f.Cancelled != 1 || f.LabelCost != (Money{5, 59573}) {
		t.Fatalf("envíos %d, cancelados %d, guías %+v", f.Shipments, f.Cancelled, f.LabelCost)
	}
}

func TestNewPeriodYFiltro(t *testing.T) {
	p, err := NewPeriod("2026-11-01", "2026-11-14")
	if err != nil {
		t.Fatal(err)
	}
	list := ShipmentList{Data: []json.RawMessage{
		json.RawMessage(`{"attributes":{"created_at":"2026-11-01T05:00:00Z","on_delivery_status":"pending"}}`), // 1 nov 00:00 en Colombia: entra
		json.RawMessage(`{"attributes":{"created_at":"2026-11-01T04:59:00Z","on_delivery_status":"pending"}}`), // 31 oct en Colombia: no
		json.RawMessage(`{"attributes":{"created_at":"2026-11-15T04:30:00Z","on_delivery_status":"pending"}}`), // 14 nov 23:30 en Colombia: entra
		json.RawMessage(`{"attributes":{"created_at":"2026-11-15T05:00:00Z","on_delivery_status":"pending"}}`), // 15 nov: no
	}}
	if got := len(financeShipments(list, p)); got != 2 {
		t.Fatalf("entraron %d envíos al rango, se esperaban 2", got)
	}
	if _, err := NewPeriod("2026-11-14", "2026-11-01"); err == nil {
		t.Fatal("desde posterior a hasta debe dar error")
	}
	if all, _ := NewPeriod("", "2026-11-14"); !all.From.IsZero() {
		t.Fatal("sin desde no debe haber límite inicial")
	}
}

func TestMovementCategory(t *testing.T) {
	casos := []struct{ kind, typ, comment, want string }{
		{"deposit", "", "Recarga de saldo", "recarga"},
		{"withdrawal", "Shipment", "Guía 240065845604", "guia"},
		{"withdrawal", "Shipment", "Protección Plus", "proteccion"},
		{"refund", "Shipment", "Cancelación", "devolucion"},
		{"withdrawal", "", "Ajuste", "otro"},
		{"withdrawal", "Shipment", "Pago de servicefee 89db279d", "servicio"},
		{"withdrawal", "", "Fee Mercado Pago #187118512635", "fee_recarga"},
		{"refund", "Shipment", "Protección Plus", "devolucion"},
	}
	for _, c := range casos {
		if got := movementCategory(c.kind, c.typ, c.comment); got != c.want {
			t.Errorf("movementCategory(%q,%q,%q) = %q, want %q", c.kind, c.typ, c.comment, got, c.want)
		}
	}
}

func TestFinanceCompleto(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":7200}`)
	})
	mux.HandleFunc("GET /api/v1/shipments", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[
			{"id":"s1","attributes":{"created_at":"2026-11-10T15:00:00Z","order_id":"o1","master_tracking_number":"240001","on_delivery_status":"collected","on_delivery_amount":72573.0,"total":"12573.0"}},
			{"id":"s2","attributes":{"created_at":"2026-11-12T15:00:00Z","order_id":"o2","master_tracking_number":"240002","on_delivery_status":"pending","on_delivery_amount":72000.0,"total":"12000.0"}}
		],"meta":{"total_pages":1}}`)
	})
	mux.HandleFunc("GET /api/v1/orders/o1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"included":[{"type":"item","attributes":{"quantity":2}}]}`)
	})
	mux.HandleFunc("GET /api/v1/orders/o2", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"included":[{"type":"item","attributes":{"quantity":1}}]}`)
	})
	mux.HandleFunc("GET /api/v1/finance/credits", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"balance":"85500.0","currency":"COP"}}`)
	})
	mux.HandleFunc("GET /api/v1/transaction_stats", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("date_from") != "2026-11-01" || r.URL.Query().Get("date_to") != "2026-11-30" {
			t.Errorf("rango de movimientos inesperado: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"data":[
			{"attributes":{"transaction_kind":"withdrawal","transaction_amount":"12573.0","transactionable_type":"Shipment","transaction_comments":"Guía","shipment_tracking_number":"G1"}},
			{"attributes":{"transaction_kind":"withdrawal","transaction_amount":"3000.0","transactionable_type":"Shipment","transaction_comments":"Protección Plus"}},
			{"attributes":{"transaction_kind":"deposit","transaction_amount":"100000.0","transaction_comments":"Recarga"}}
		],"meta":{"next_page":null}}`)
	})
	mux.HandleFunc("GET /api/v1/finance/extra-charges", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"amount":"5000","charge_type":"ExtraCharge::Overweight","tracking_number":"G1","detection_date":"2026-11-11T10:00:00-05:00"}],"meta":{"next_page":null}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(config.Config{SkydropxMode: "live", SkydropxBaseURL: srv.URL, SkydropxClientID: "id", SkydropxClientSecret: "s"})

	p, _ := NewPeriod("2026-11-01", "2026-11-30")
	f, err := c.Finance(context.Background(), p, FlatCommission(15000))
	if err != nil {
		t.Fatal(err)
	}
	if f.Balance == nil || *f.Balance != 85500 {
		t.Fatalf("saldo = %v", f.Balance)
	}
	if f.Commission.PaidAmount != 30000 || f.Commission.PendingAmount != 15000 {
		t.Fatalf("comisión = %+v, se esperaba 30000 por cobrados y 15000 por cobrar", f.Commission)
	}
	if f.Spending["guia"].Amount != 12573 || f.Spending["proteccion"].Amount != 3000 || f.Spending["recarga"].Amount != 100000 {
		t.Fatalf("gastos agrupados inesperados: %+v", f.Spending)
	}
	if f.Extra != (Money{1, 5000}) || f.ExtraCharges[0].Type != "Overweight" {
		t.Fatalf("cargos extra inesperados: %+v %+v", f.Extra, f.ExtraCharges)
	}
	if len(f.Warnings) != 0 {
		t.Fatalf("no debería haber avisos: %v", f.Warnings)
	}
}

func TestPerShipmentJuntaDevolucionesPorGuia(t *testing.T) {
	shipments := []financeShipment{
		{Date: "2026-11-02T10:00:00Z", Tracking: "G1", Customer: "Ana", OnDelivery: "collected", CODAmount: 72000, Cost: 12000},
		{Date: "2026-11-03T10:00:00Z", Tracking: "G2", Customer: "Luis", OnDelivery: "pending", CODAmount: 70000, Cost: 10000},
		{Date: "2026-11-01T10:00:00Z", Tracking: "G3", OnDelivery: "failed", CODAmount: 70000, Cost: 10000},
	}
	movs := []Movement{
		{Date: "2026-11-02T10:00:00Z", Kind: "withdrawal", Category: "guia", Amount: 12000, Tracking: "G1"}, // cobro de la guía: no es devolución
		{Date: "2026-11-02T10:00:00Z", Kind: "withdrawal", Category: "servicio", Amount: 300, Tracking: "G1"},
		{Date: "2026-11-09T10:00:00Z", Kind: "refund", Amount: 12000, Tracking: "G1"},
		{Date: "2026-11-05T10:00:00Z", Kind: "deposit", Amount: 50000}, // recarga sin guía
	}
	extras := []ExtraCharge{{Date: "2026-11-08", Tracking: "G3", Amount: 9000}}

	rows, total := perShipment(shipments, movs, extras)
	if len(rows) != 3 || rows[0].Tracking != "G2" || rows[2].Tracking != "G3" {
		t.Fatalf("orden inesperado: %+v", rows)
	}
	g1 := rows[1]
	if g1.Returned != 12000 || g1.ReturnedAt != "2026-11-09T10:00:00Z" || g1.Customer != "Ana" {
		t.Fatalf("G1: %+v", g1)
	}
	if g1.Charged != 12300 || g1.ChargedParts["servicio"] != 300 {
		t.Fatalf("G1 descontado %v %+v", g1.Charged, g1.ChargedParts)
	}
	if rows[2].Extra != 9000 || rows[0].Returned != 0 {
		t.Fatalf("G3 extra %v, G2 devuelto %v", rows[2].Extra, rows[0].Returned)
	}
	if total != (Money{1, 12000}) {
		t.Fatalf("total devuelto %+v", total)
	}
}

func TestFinanceShipmentsNombreYGuia(t *testing.T) {
	p, _ := NewPeriod("2026-11-01", "2026-11-14")
	list := ShipmentList{
		Data: []json.RawMessage{json.RawMessage(`{"relationships":{"address_to":{"data":{"id":"a1","type":"address"}}},
			"attributes":{"created_at":"2026-11-02T15:00:00Z","master_tracking_number":"240001","on_delivery_status":"pending"}}`)},
		Included: []json.RawMessage{json.RawMessage(`{"id":"a1","type":"address","attributes":{"name":"Ana Pérez"}}`)},
	}
	got := financeShipments(list, p)
	if len(got) != 1 || got[0].Customer != "Ana Pérez" || got[0].Tracking != "240001" {
		t.Fatalf("envío inesperado: %+v", got)
	}
}

func TestEnviosSinGuiaQuedanFuera(t *testing.T) {
	p, _ := NewPeriod("2026-11-13", "2026-11-13")
	list := ShipmentList{Data: []json.RawMessage{
		json.RawMessage(`{"attributes":{"created_at":"2026-11-13T15:00:00Z","master_tracking_number":"240065822548","on_delivery_status":"pending","total":"16532"}}`),
		json.RawMessage(`{"attributes":{"created_at":"2026-11-13T14:50:00Z","master_tracking_number":null,"total":"16532"}}`), // el intento que falló
	}}
	got := withLabel(financeShipments(list, p))
	if len(got) != 1 || got[0].Tracking != "240065822548" {
		t.Fatalf("envíos con guía: %+v", got)
	}
}

func TestCustomerPaysConYSinEnvioIncluido(t *testing.T) {
	// Manuel: Skydropx reporta 72.573, que ya trae el envío (60.000 + 12.573).
	manuel := financeShipment{Product: 60000, CODAmount: 72573, Cost: 12266}
	if got := manuel.customerPays(); got != 72573 {
		t.Fatalf("Manuel paga %v, se esperaba 72573", got)
	}
	// Aracely: Skydropx reporta 120.000, solo el producto; el cliente paga además el envío.
	aracely := financeShipment{Product: 120000, CODAmount: 120000, Cost: 16532}
	if got := aracely.customerPays(); got != 136532 {
		t.Fatalf("Aracely paga %v, se esperaba 136532", got)
	}
	// Sin el producto de la orden se confía en lo reportado.
	if got := (financeShipment{CODAmount: 72573, Cost: 12266}).customerPays(); got != 72573 {
		t.Fatalf("sin producto: %v", got)
	}
}

func TestComisionSegunFechaDelEnvio(t *testing.T) {
	cambio, _ := time.ParseInLocation("2006-01-02", "2026-11-15", colombia)
	rate := func(t time.Time) float64 {
		if t.Before(cambio) {
			return 15000
		}
		return 12000
	}
	shipments := []financeShipment{
		{Date: "2026-11-14T20:00:00Z", Tracking: "G1", OrderID: "o1", OnDelivery: "collected"}, // 2 datáfonos a 15.000
		{Date: "2026-11-16T15:00:00Z", Tracking: "G2", OrderID: "o2", OnDelivery: "collected"}, // 1 datáfono a 12.000
		{Date: "2026-11-16T16:00:00Z", Tracking: "G3", OrderID: "o3", OnDelivery: "pending"},   // 1 datáfono a 12.000
	}
	f := summarizeShipments(shipments, map[string]int{"o1": 2, "o2": 1, "o3": 1}, rate)
	if f.Commission.PaidAmount != 42000 || f.Commission.PendingAmount != 12000 {
		t.Fatalf("comisión %+v, se esperaba 42000 cobrados y 12000 por cobrar", f.Commission)
	}
}
