package bot

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skydropx-automator/internal/skydropx"
)

func TestMarkCODDoesNotNotifyTwice(t *testing.T) {
	st, err := loadState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fresh, _ := st.markCOD("s1", "collected"); !fresh {
		t.Fatal("la entrega debe avisar el pago")
	}
	if fresh, _ := st.markCOD("s1", "collected"); fresh {
		t.Fatal("un webhook repetido no debe avisar otra vez")
	}
	// The state survives a restart.
	again, err := loadState(st.path)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, _ := again.markCOD("s1", "collected"); fresh {
		t.Fatal("después de reiniciar no debe repetir el aviso")
	}
}

func TestCODMessage(t *testing.T) {
	e := skydropx.CODShipment{Tracking: "240065822548", Customer: "Aracely cedeño macias", Status: "collected", Cost: 16532}
	if v := codSellerMessage(e, 136532, 120000); strings.Contains(v, "saldo disponible") || !strings.Contains(v, "¡Pagaron!") ||
		!strings.Contains(v, "el cliente pagó") {
		t.Errorf("aviso al vendedor:\n%s", v)
	}
	f := e
	f.Status, f.Phone, f.City = "failed", "3001234567", "Montería"
	if v := codSellerMessage(f, 136532, 120000); !strings.Contains(v, "No se pudo cobrar") || !strings.Contains(v, "3001234567") ||
		!strings.Contains(v, "Montería") || !strings.Contains(v, "Llama al cliente") || strings.Contains(v, "cargo por la devolución") {
		t.Errorf("cobro fallido al vendedor:\n%s", v)
	}
	if m := codMessage(f, 136532, 120000); !strings.Contains(m, "3001234567") || !strings.Contains(m, "cargo por la devolución") {
		t.Errorf("cobro fallido a gestión:\n%s", m)
	}
	msg := codMessage(e, 136532, 120000)
	for _, want := range []string{"¡Pagaron!", "Aracely cedeño macias", "240065822548", "$136.532", "producto $120.000 + envío $16.532"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	e.Status = "failed"
	if msg := codMessage(e, 136532, 0); !strings.Contains(msg, "No se pudo cobrar") || !strings.Contains(msg, "producto + envío") {
		t.Errorf("mensaje de no cobrado inesperado:\n%s", msg)
	}
}

func TestIncidentMessage(t *testing.T) {
	e := skydropx.CODShipment{Tracking: "240065822548", Customer: "Aracely", Phone: "3001234567", City: "Montería"}
	msg := incidentMessage(e, "delivery_attempt", "Destinatario ausente", "https://siguetuenvio.interrapidisimo.com/")
	for _, want := range []string{"No pudieron entregar", "Aracely", "3001234567", "Montería", "240065822548", "Destinatario ausente", "Llama al cliente"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if msg := incidentMessage(e, "delivered_to_branch", "", ""); !strings.Contains(msg, "quedó en la oficina") {
		t.Errorf("mensaje de oficina inesperado:\n%s", msg)
	}
}

func TestMarkNoticeOncePerDay(t *testing.T) {
	st, _ := loadState(filepath.Join(t.TempDir(), "state.json"))
	if ok, _ := st.markNotice("s1:delivery_attempt:2026-11-14", "2026-11-14"); !ok {
		t.Fatal("el primer aviso del día debe enviarse")
	}
	if ok, _ := st.markNotice("s1:delivery_attempt:2026-11-14", "2026-11-14"); ok {
		t.Fatal("el mismo aviso no debe repetirse el mismo día")
	}
	if ok, _ := st.markNotice("s1:delivery_attempt:2026-11-15", "2026-11-15"); !ok {
		t.Fatal("un nuevo intento al día siguiente sí se avisa")
	}
	// Notices older than 30 days are forgotten.
	_, _ = st.markNotice("s2:exception:2026-12-20", "2026-12-20")
	if _, ok := st.IssueNotices["s1:delivery_attempt:2026-11-14"]; ok {
		t.Fatal("los avisos viejos deben borrarse")
	}
}

func TestSummaryDueOncePerDay(t *testing.T) {
	st, _ := loadState(filepath.Join(t.TempDir(), "state.json"))
	if ok, _ := st.summaryDue(false, "2026-11-14"); !ok {
		t.Fatal("el resumen de hoy debe enviarse")
	}
	if ok, _ := st.summaryDue(false, "2026-11-14"); ok {
		t.Fatal("no debe repetirse el mismo día")
	}
	if ok, _ := st.summaryDue(true, "2026-11-14"); !ok {
		t.Fatal("el semanal se lleva aparte del diario")
	}
}

func TestSummaryText(t *testing.T) {
	bal := 85500.0
	f := skydropx.Finance{
		Balance:    &bal,
		Collected:  skydropx.Money{Count: 2, Amount: 145146},
		Pending:    skydropx.Money{Count: 1, Amount: 136532},
		Commission: skydropx.Commission{PaidAmount: 30000, PendingAmount: 30000},
	}
	today := []skydropx.ShipmentMoney{
		{Status: "pending", LabelCost: 16532, Charged: 21635},
		{Status: "cancelled", LabelCost: 12266, Charged: 15363, Returned: 15363}, // cancelled: doesn't count
	}
	msg := summaryText("📊 <b>Resumen del 14 nov</b>", f, today, "Hoy")
	for _, want := range []string{"Guías generadas: 1 · pagaste $21.635", "En lo que va del mes", "Cobrado: 2 · $145.146",
		"Por cobrar: 1 · $136.532", "$30.000 de lo cobrado", "Saldo en créditos: $85.500"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
}

func TestPickupNotice(t *testing.T) {
	e := skydropx.CODShipment{Tracking: "240065822548", Customer: "Aracely", City: "Montería"}
	msg := incidentMessage(e, "picked_up", "", "https://siguetuenvio.interrapidisimo.com/")
	for _, want := range []string{"La transportadora recibió el paquete", "Aracely", "240065822548", "Ya va en camino"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
}

func TestIssueMessages(t *testing.T) {
	if msgs := issueMessages(nil, 12); len(msgs) != 1 || !strings.Contains(msgs[0], "Sin novedades") {
		t.Fatalf("sin problemas: %q", msgs)
	}
	problems := []skydropx.Problem{
		{CODShipment: skydropx.CODShipment{Tracking: "G1", Customer: "Ana", Phone: "300"}, Kind: "intento", LastEvent: "Destinatario ausente", LastDate: "2026-11-13T15:00:00Z"},
		{CODShipment: skydropx.CODShipment{Tracking: "G2", Customer: "Luis"}, Kind: "devolucion", LastEvent: "En devolución"},
	}
	msgs := issueMessages(problems, 20)
	if len(msgs) != 3 {
		t.Fatalf("se esperaban encabezado + 2 grupos: %q", msgs)
	}
	// Returns go first because they are the most urgent.
	if !strings.Contains(msgs[1], "En devolución o sin cobrar") || !strings.Contains(msgs[1], "Luis") {
		t.Fatalf("primer grupo inesperado: %s", msgs[1])
	}
	if !strings.Contains(msgs[2], "No pudieron entregar") || !strings.Contains(msgs[2], "📞 300") || !strings.Contains(msgs[2], "(13 nov)") {
		t.Fatalf("segundo grupo inesperado: %s", msgs[2])
	}
	// Many shipments are split into several messages that fit in Telegram.
	var many []skydropx.Problem
	for i := 0; i < 80; i++ {
		many = append(many, skydropx.Problem{CODShipment: skydropx.CODShipment{Tracking: "240065822548", Customer: "Cliente con un nombre largo", Phone: "3001234567", City: "Montería"}, Kind: "intento", LastEvent: "Destinatario ausente, se reintenta mañana"})
	}
	for _, m := range issueMessages(many, 80) {
		if len(m) > 4096 {
			t.Fatalf("mensaje de %d caracteres supera el límite de Telegram", len(m))
		}
	}
}

func TestIssueShowsHistory(t *testing.T) {
	arrived, _ := time.Parse(time.RFC3339, "2026-11-14T15:30:00-05:00")
	inTransit, _ := time.Parse(time.RFC3339, "2026-11-13T09:00:00-05:00")
	p := skydropx.Problem{
		CODShipment: skydropx.CODShipment{Tracking: "240065695027", Customer: "Yhon Jarby Torres Rivera"},
		Kind:        "oficina",
		History: []skydropx.TrackEvent{
			{Date: arrived, Description: "Reclame en oficina"},
			{Date: inTransit, Description: "En tránsito a ciudad destino"},
		},
	}
	item := issueItem(p)
	for _, want := range []string{"Últimos eventos", "14 nov 3:30 p. m. — Reclame en oficina", "13 nov 9:00 a. m. — En tránsito a ciudad destino"} {
		if !strings.Contains(item, want) {
			t.Errorf("falta %q en:\n%s", want, item)
		}
	}
}
