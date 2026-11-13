package bot

import (
	"encoding/json"
	"strings"
	"testing"

	"skydropx-automator/internal/skydropx"
)

// Envío de ejemplo con datos ficticios, con la misma forma que devuelve GET /api/v1/shipments.
const fixtureShipment = `{
  "id": "envio-1",
  "type": "shipments",
  "attributes": {
    "order_id": "orden-1",
    "master_tracking_number": "240000000001",
    "carrier_name": "interrapidisimo",
    "service_name": "Mensajeria Express"
  },
  "relationships": {
    "address_to": {"data": {"id": "dir-1", "type": "address"}},
    "packages": {"data": [{"id": "pkg-1", "type": "package"}]}
  }
}`

const fixtureIncluded = `[
  {"id": "dir-1", "type": "address", "attributes": {"name": "Cliente de Prueba", "area_level2": "BELLO", "area_level1": "ANTIOQUIA"}},
  {"id": "pkg-1", "type": "package", "attributes": {"label_url": "https://ejemplo.test/guia.pdf"}}
]`

func TestGuiaListaSeEncuentraPorOrden(t *testing.T) {
	list := skydropx.ShipmentList{
		Data:     []json.RawMessage{json.RawMessage(fixtureShipment)},
		Included: decodeList(t, fixtureIncluded),
	}
	idx := parseShipments(list)

	sh, ok := idx.byOrder("orden-1")
	if !ok {
		t.Fatal("no encontró el envío de la orden orden-1")
	}
	if sh.tracking != "240000000001" || sh.nombre != "Cliente de Prueba" || sh.label != "https://ejemplo.test/guia.pdf" {
		t.Fatalf("datos del envío incorrectos: %+v", sh)
	}
	if _, ok := idx.byOrder("otra-orden"); ok {
		t.Fatal("no debería encontrar una orden que no existe")
	}

	msg := sh.message()
	for _, esperado := range []string{"Guía lista", "240000000001", "Cliente de Prueba", "Descargar guía (PDF)"} {
		if !strings.Contains(msg, esperado) {
			t.Errorf("el mensaje no incluye %q: %s", esperado, msg)
		}
	}
}

func TestFormatCOP(t *testing.T) {
	if got := formatCOP(25000); got != "$25.000" {
		t.Errorf("formatCOP(25000) = %q", got)
	}
	if got := formatCOP(1234567); got != "$1.234.567" {
		t.Errorf("formatCOP(1234567) = %q", got)
	}
}

func decodeList(t *testing.T, raw string) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("fixture inválido: %v", err)
	}
	return out
}

func TestEnvioDeDetalleSePuedeLeer(t *testing.T) {
	raw := json.RawMessage(`{"data": ` + fixtureShipment + `, "included": ` + fixtureIncluded + `}`)
	list, err := shipmentListFromDetail(raw)
	if err != nil {
		t.Fatalf("no leyó el detalle del envío: %v", err)
	}
	idx := parseShipments(list)
	if len(idx) != 1 || idx[0].tracking != "240000000001" || idx[0].label == "" {
		t.Fatalf("detalle mal interpretado: %+v", idx)
	}
}
