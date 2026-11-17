package bot

import (
	"encoding/json"
	"strings"
	"testing"

	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/skydropx"
)

// Sample shipment with fake data, in the same shape GET /api/v1/shipments returns.
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

func TestReadyLabelIsFoundByOrder(t *testing.T) {
	list := skydropx.ShipmentList{
		Data:     []json.RawMessage{json.RawMessage(fixtureShipment)},
		Included: decodeList(t, fixtureIncluded),
	}
	idx := parseShipments(list)

	sh, ok := idx.byOrder("orden-1")
	if !ok {
		t.Fatal("no encontró el envío de la orden orden-1")
	}
	if sh.tracking != "240000000001" || sh.fullName != "Cliente de Prueba" || sh.label != "https://ejemplo.test/guia.pdf" {
		t.Fatalf("datos del envío incorrectos: %+v", sh)
	}
	if _, ok := idx.byOrder("otra-orden"); ok {
		t.Fatal("no debería encontrar una orden que no existe")
	}

	msg := sh.message()
	for _, wantText := range []string{"Envío creado con éxito", "240000000001", "Cliente de Prueba", "Interrapidísimo - Mensajeria Express", "https://siguetuenvio.interrapidisimo.com/", "https://ejemplo.test/guia.pdf"} {
		if !strings.Contains(msg, wantText) {
			t.Errorf("el mensaje no incluye %q: %s", wantText, msg)
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

func TestDetailShipmentIsReadable(t *testing.T) {
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

func TestFingerprintIgnoresFormat(t *testing.T) {
	a := domain.ShipmentData{Document: "1.077.458.155", Phone: "314 561 5105", City: "Quibdó", Address: "Carrera 7 # 30", Quantity: 1, UnitPrice: 60000}
	b := domain.ShipmentData{Document: "1077458155", Phone: "3145615105", City: "QUIBDO", Address: "carrera 7 #30", Quantity: 1, UnitPrice: 60000}
	if fingerprint(a) != fingerprint(b) {
		t.Fatalf("el mismo pedido con distinto formato debería tener la misma huella:\n%s\n%s", fingerprint(a), fingerprint(b))
	}
	b.Quantity = 2
	if fingerprint(a) == fingerprint(b) {
		t.Fatal("otra cantidad debería ser otro pedido")
	}
}

func TestUndoRepeat(t *testing.T) {
	text, force := undoRepeat("*REPETIR*\n*Receptor:* Ana")
	if !force || text != "*Receptor:* Ana" {
		t.Fatalf("got %q, %v", text, force)
	}
	if _, force := undoRepeat("*Receptor:* Ana"); force {
		t.Fatal("sin REPETIR no debería forzar")
	}
}
