package bot

import (
	"path/filepath"
	"strings"
	"testing"

	"skydropx-automator/internal/skydropx"
)

func TestMarcarCobroNoAvisaDosVeces(t *testing.T) {
	st, err := loadState(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if nuevo, _ := st.marcarCobro("s1", "collected"); !nuevo {
		t.Fatal("la entrega debe avisar el pago")
	}
	if nuevo, _ := st.marcarCobro("s1", "collected"); nuevo {
		t.Fatal("un webhook repetido no debe avisar otra vez")
	}
	// El estado sobrevive al reinicio.
	again, err := loadState(st.path)
	if err != nil {
		t.Fatal(err)
	}
	if nuevo, _ := again.marcarCobro("s1", "collected"); nuevo {
		t.Fatal("después de reiniciar no debe repetir el aviso")
	}
}

func TestCobroMensaje(t *testing.T) {
	e := skydropx.CODShipment{Tracking: "240065822548", Customer: "Aracely cedeño macias", Status: "collected", Cost: 16532}
	msg := cobroMensaje(e, 136532, 120000)
	for _, want := range []string{"¡Pagaron!", "Aracely cedeño macias", "240065822548", "$136.532", "producto $120.000 + envío $16.532"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	e.Status = "failed"
	if msg := cobroMensaje(e, 136532, 0); !strings.Contains(msg, "No se pudo cobrar") || !strings.Contains(msg, "producto + envío") {
		t.Errorf("mensaje de no cobrado inesperado:\n%s", msg)
	}
}

func TestIncidenciaMensaje(t *testing.T) {
	e := skydropx.CODShipment{Tracking: "240065822548", Customer: "Aracely", Phone: "3001234567", City: "Montería"}
	msg := incidenciaMensaje(e, "delivery_attempt", "Destinatario ausente", "https://siguetuenvio.interrapidisimo.com/")
	for _, want := range []string{"No pudieron entregar", "Aracely", "3001234567", "Montería", "240065822548", "Destinatario ausente", "Llama al cliente"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
	if msg := incidenciaMensaje(e, "delivered_to_branch", "", ""); !strings.Contains(msg, "quedó en la oficina") {
		t.Errorf("mensaje de oficina inesperado:\n%s", msg)
	}
}

func TestMarcarAvisoUnaVezPorDia(t *testing.T) {
	st, _ := loadState(filepath.Join(t.TempDir(), "state.json"))
	if ok, _ := st.marcarAviso("s1:delivery_attempt:2026-11-14", "2026-11-14"); !ok {
		t.Fatal("el primer aviso del día debe enviarse")
	}
	if ok, _ := st.marcarAviso("s1:delivery_attempt:2026-11-14", "2026-11-14"); ok {
		t.Fatal("el mismo aviso no debe repetirse el mismo día")
	}
	if ok, _ := st.marcarAviso("s1:delivery_attempt:2026-11-15", "2026-11-15"); !ok {
		t.Fatal("un nuevo intento al día siguiente sí se avisa")
	}
	// Los avisos de hace más de 30 días se olvidan.
	_, _ = st.marcarAviso("s2:exception:2026-12-20", "2026-12-20")
	if _, ok := st.Avisos["s1:delivery_attempt:2026-11-14"]; ok {
		t.Fatal("los avisos viejos deben borrarse")
	}
}

func TestResumenPendienteUnoPorDia(t *testing.T) {
	st, _ := loadState(filepath.Join(t.TempDir(), "state.json"))
	if ok, _ := st.resumenPendiente(false, "2026-11-14"); !ok {
		t.Fatal("el resumen de hoy debe enviarse")
	}
	if ok, _ := st.resumenPendiente(false, "2026-11-14"); ok {
		t.Fatal("no debe repetirse el mismo día")
	}
	if ok, _ := st.resumenPendiente(true, "2026-11-14"); !ok {
		t.Fatal("el semanal se lleva aparte del diario")
	}
}

func TestResumenTexto(t *testing.T) {
	bal := 85500.0
	f := skydropx.Finance{
		Balance:    &bal,
		Collected:  skydropx.Money{Count: 2, Amount: 145146},
		Pending:    skydropx.Money{Count: 1, Amount: 136532},
		Commission: skydropx.Commission{PaidAmount: 30000, PendingAmount: 30000},
	}
	hoy := []skydropx.ShipmentMoney{
		{Status: "pending", LabelCost: 16532, Charged: 21635},
		{Status: "cancelled", LabelCost: 12266, Charged: 15363, Returned: 15363}, // cancelada: no cuenta
	}
	msg := resumenTexto("📊 <b>Resumen del 14 nov</b>", f, hoy, "Hoy")
	for _, want := range []string{"Guías generadas: 1 · pagaste $21.635", "En lo que va del mes", "Cobrado: 2 · $145.146",
		"Por cobrar: 1 · $136.532", "$30.000 de lo cobrado", "Saldo en créditos: $85.500"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
}

func TestAvisoRecogida(t *testing.T) {
	e := skydropx.CODShipment{Tracking: "240065822548", Customer: "Aracely", City: "Montería"}
	msg := incidenciaMensaje(e, "picked_up", "", "https://siguetuenvio.interrapidisimo.com/")
	for _, want := range []string{"La transportadora recibió el paquete", "Aracely", "240065822548", "Ya va en camino"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta %q en:\n%s", want, msg)
		}
	}
}
