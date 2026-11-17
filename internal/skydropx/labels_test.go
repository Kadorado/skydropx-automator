package skydropx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"skydropx-automator/internal/config"
	"skydropx-automator/internal/domain"
)

// fakeSkydropx simulates the API: postal code 130001 "doesn't exist" and 130002 does.
type fakeSkydropx struct {
	mu        sync.Mutex
	quoted    []string       // quoted postal codes, in order
	quoteBody map[string]any // last quote received
	shipment  map[string]any // shipment received
}

func (f *fakeSkydropx) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":7200}`)
	})
	mux.HandleFunc("POST /api/v1/quotations", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		q := body["quotation"].(map[string]any)
		postal := q["address_to"].(map[string]any)["postal_code"].(string)
		f.mu.Lock()
		f.quoted = append(f.quoted, postal)
		f.quoteBody = q
		f.mu.Unlock()
		if postal == "130001" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"errors":{"address_to":{"postal_code":["no existe"]}}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"q1"}`)
	})
	mux.HandleFunc("GET /api/v1/quotations/q1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"is_completed":true,"rates":[
			{"id":"r-cara","success":true,"provider_name":"interrapidisimo","provider_service_name":"Mensajería","total":"21000.0","days":3},
			{"id":"r-barata","success":true,"provider_name":"interrapidisimo","provider_service_name":"Carga","total":14500,"days":2},
			{"id":"r-otra","success":true,"provider_name":"coordinadora","total":"9000"},
			{"id":"r-error","success":false,"provider_name":"interrapidisimo","error_messages":["sin cobertura"]}
		]}`)
	})
	mux.HandleFunc("POST /api/v1/shipments/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.shipment = body["shipment"].(map[string]any)
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"data":{"id":"shp-1"}}`)
	})
	return mux
}

func newTestClient(t *testing.T, f *fakeSkydropx) *Client {
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return NewClient(config.Config{
		SkydropxMode:            "live",
		SkydropxBaseURL:         srv.URL,
		SkydropxClientID:        "id",
		SkydropxClientSecret:    "secret",
		OriginAddressTemplateID: "tpl-origen",
		OriginCountry:           "CO",
		OriginEmail:             "tienda@example.com",
		OriginName:              "Tienda",
		DefaultWeightKg:         1,
		DefaultLengthCm:         10,
		DefaultWidthCm:          10,
		DefaultHeightCm:         15,
		DefaultContent:          "datafono",
	})
}

func TestQuoteLabelAndCreateLabel(t *testing.T) {
	f := &fakeSkydropx{}
	c := newTestClient(t, f)
	data := domain.ShipmentData{
		Name: "Dagoberto Prada Padilla", Document: "73576742", Phone: "3002013421",
		Department: "Bolívar", City: "Cartagena", Neighborhood: "Villa Estrella",
		Address: "Cr 92#43-75", Quantity: 1, UnitPrice: 60000, PostalCode: "130001",
	}

	q, err := c.QuoteLabel(context.Background(), data)
	if err != nil {
		t.Fatalf("QuoteLabel: %v", err)
	}
	if q.RateID != "r-barata" || q.Total != 14500 || q.Days != 2 || q.PostalCode != "130002" {
		t.Fatalf("cotización inesperada: %+v", q)
	}
	if strings.Join(f.quoted, ",") != "130001,130002" {
		t.Fatalf("códigos cotizados = %v, se esperaba 130001 y luego 130002", f.quoted)
	}
	if carriers, _ := json.Marshal(f.quoteBody["requested_carriers"]); string(carriers) != `["interrapidisimo"]` {
		t.Fatalf("requested_carriers = %s", carriers)
	}
	if id := f.quoteBody["address_from"].(map[string]any)["address_template_id"]; id != "tpl-origen" {
		t.Fatalf("el origen debe usar la dirección guardada, llegó %v", id)
	}

	id, err := c.CreateLabel(context.Background(), "orden-1", q, data)
	if err != nil || id != "shp-1" {
		t.Fatalf("CreateLabel = %q, %v", id, err)
	}
	s := f.shipment
	if s["rate_id"] != "r-barata" || s["order_id"] != "orden-1" || s["unique_shipment"] != true {
		t.Fatalf("envío inesperado: %v", s)
	}
	pkg := s["packages"].([]any)[0].(map[string]any)
	if pkg["package_protected"] != false {
		t.Fatalf("package_protected = %v, debe ir false (sin Protección Plus)", pkg["package_protected"])
	}
	to := s["address_to"].(map[string]any)
	if to["email"] != "tienda@example.com" {
		t.Fatalf("sin correo del cliente debe usar el de la tienda, llegó %v", to["email"])
	}
	if to["name"] != "Dagoberto Prada Padilla" || len([]rune(to["name"].(string))) > 30 {
		t.Fatalf("nombre inesperado: %v", to["name"])
	}
}

func TestPostalCandidates(t *testing.T) {
	got := postalCandidates("130001")
	if len(got) != maxPostalSuffix-1 || got[0] != "130002" || got[len(got)-1] != "130020" {
		t.Fatalf("postalCandidates(130001) = %v", got)
	}
	if got := postalCandidates("05001"); got != nil {
		t.Fatalf("postalCandidates(05001) = %v, want nil", got)
	}
}

func TestLabelsSince(t *testing.T) {
	list := ShipmentList{
		Data: []json.RawMessage{
			// Today in Colombia (comes in UTC): yes.
			json.RawMessage(`{"attributes":{"created_at":"2026-11-14T22:30:00Z","master_tracking_number":"B"},"relationships":{"packages":{"data":[{"id":"p2"}]}}}`),
			json.RawMessage(`{"attributes":{"created_at":"2026-11-14T08:00:00-05:00","master_tracking_number":"A"},"relationships":{"packages":{"data":[{"id":"p1"}]}}}`),
			// Yesterday in Colombia even though it is already the 14th in UTC: no.
			json.RawMessage(`{"attributes":{"created_at":"2026-11-14T03:00:00Z","master_tracking_number":"AYER"},"relationships":{"packages":{"data":[{"id":"p3"}]}}}`),
			// Today but without a PDF yet: no.
			json.RawMessage(`{"attributes":{"created_at":"2026-11-14T10:00:00-05:00","master_tracking_number":"SINPDF"},"relationships":{"packages":{"data":[{"id":"p4"}]}}}`),
		},
		Included: []json.RawMessage{
			json.RawMessage(`{"id":"p1","type":"package","attributes":{"label_url":"https://x/a.pdf"}}`),
			json.RawMessage(`{"id":"p2","type":"package","attributes":{"label_url":"https://x/b.pdf"}}`),
			json.RawMessage(`{"id":"p3","type":"package","attributes":{"label_url":"https://x/c.pdf"}}`),
			json.RawMessage(`{"id":"p4","type":"package","attributes":{}}`),
		},
	}
	now := time.Date(2026, 11, 14, 20, 0, 0, 0, colombia)
	got := labelsSince(list, now, 1)
	if len(got) != 2 || got[0].Tracking != "A" || got[1].Tracking != "B" {
		t.Fatalf("solo hoy = %+v, se esperaban A y B en orden", got)
	}
	// With 2 days yesterday's is included too, and comes first as the oldest.
	got = labelsSince(list, now, 2)
	if len(got) != 3 || got[0].Tracking != "AYER" {
		t.Fatalf("últimos 2 días = %+v, se esperaban AYER, A y B", got)
	}
}

func TestSaveRecipientAddress(t *testing.T) {
	var mu sync.Mutex
	var savedAliases []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":7200}`)
	})
	mux.HandleFunc("GET /api/v1/address_templates", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var data []string
		for _, g := range savedAliases {
			attrs := g["address_attributes"].(map[string]any)
			data = append(data, `{"alias_name":"`+g["alias_name"].(string)+`","address_type":"to","address":{"street1":"`+attrs["street1"].(string)+`"}}`)
		}
		_, _ = io.WriteString(w, `{"data":[`+strings.Join(data, ",")+`],"meta":{"next_page":null}}`)
	})
	mux.HandleFunc("POST /api/v1/address_templates/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		savedAliases = append(savedAliases, body["address_template"].(map[string]any))
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"data":{"id":"tpl-1"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(config.Config{SkydropxMode: "live", SkydropxBaseURL: srv.URL, SkydropxClientID: "id",
		SkydropxClientSecret: "secret", OriginEmail: "tienda@example.com"})

	data := domain.ShipmentData{Name: "Dagoberto Prada", Phone: "3002013421", Department: "Bolívar",
		City: "Cartagena", Neighborhood: "Villa Estrella", Address: "Cr 92#43-75"}
	for i := 0; i < 2; i++ { // the second attempt must not duplicate it
		if err := c.SaveRecipientAddress(context.Background(), data, "130002"); err != nil {
			t.Fatal(err)
		}
	}
	if len(savedAliases) != 1 {
		t.Fatalf("se guardaron %d direcciones, debe ser 1", len(savedAliases))
	}
	g := savedAliases[0]
	attrs := g["address_attributes"].(map[string]any)
	if g["alias_name"] != "Dagoberto Prada" || g["address_type"] != "to" || g["default"] != nil {
		t.Fatalf("plantilla inesperada: %v", g)
	}
	if attrs["postal_code"] != "130002" || attrs["email"] != "tienda@example.com" || attrs["reference"] != "Villa Estrella" {
		t.Fatalf("dirección inesperada: %v", attrs)
	}
}
