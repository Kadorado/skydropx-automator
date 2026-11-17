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

func TestKeys(t *testing.T) {
	if PhoneKey("+57 300-123-4567") != "3001234567" || PhoneKey("12") != "" {
		t.Error("ClaveTelefono")
	}
	if DocumentKey("1.234.567.890") != "1234567890" || DocumentKey("12") != "" {
		t.Error("ClaveDocumento")
	}
}

func TestCustomerHistory(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":7200}`)
	})
	// Ana (document 111, phone 3001112233): a return, a delivery that waited 5 days at the office and one that
	// waited 1 day. Someone else uses her phone. A third person is unrelated.
	mux.HandleFunc("GET /api/v1/shipments", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[
			{"id":"s1","attributes":{"created_at":"2026-11-01T10:00:00-05:00","master_tracking_number":"G1","carrier_name":"inter","on_delivery_status":"failed"},"relationships":{"address_to":{"data":{"id":"a1"}}}},
			{"id":"s2","attributes":{"created_at":"2026-10-20T10:00:00-05:00","master_tracking_number":"G2","carrier_name":"inter","on_delivery_status":"collected"},"relationships":{"address_to":{"data":{"id":"a2"}}}},
			{"id":"s3","attributes":{"created_at":"2026-10-10T10:00:00-05:00","master_tracking_number":"G3","carrier_name":"inter","on_delivery_status":"collected"},"relationships":{"address_to":{"data":{"id":"a3"}}}},
			{"id":"s4","attributes":{"created_at":"2026-10-05T10:00:00-05:00","master_tracking_number":"G4","carrier_name":"inter","on_delivery_status":"pending"},"relationships":{"address_to":{"data":{"id":"a4"}}}},
			{"id":"s5","attributes":{"created_at":"2026-10-01T10:00:00-05:00","master_tracking_number":"G5","carrier_name":"inter","on_delivery_status":"failed"},"relationships":{"address_to":{"data":{"id":"a5"}}}}
		],"included":[
			{"id":"a1","type":"address","attributes":{"name":"Ana","phone":"3001112233","tax_id_number":"111111"}},
			{"id":"a2","type":"address","attributes":{"name":"Ana","phone":"300 111 2233","tax_id_number":"111111"}},
			{"id":"a3","type":"address","attributes":{"name":"Ana","phone":"3001112233","tax_id_number":"111111"}},
			{"id":"a4","type":"address","attributes":{"name":"Primo de Ana","phone":"+573001112233","tax_id_number":"222222"}},
			{"id":"a5","type":"address","attributes":{"name":"Otro","phone":"3109998877","tax_id_number":"333333"}}
		],"meta":{"total_pages":1}}`)
	})
	mux.HandleFunc("GET /api/v1/shipments/tracking", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("tracking_number") {
		case "G2":
			_, _ = io.WriteString(w, `{"data":[
				{"attributes":{"status":"delivered_to_branch","date":"2026-10-22T09:00:00-05:00"}},
				{"attributes":{"status":"delivered","date":"2026-10-27T15:00:00-05:00"}}]}`)
		default:
			_, _ = io.WriteString(w, `{"data":[
				{"attributes":{"status":"delivered_to_branch","date":"2026-10-12T09:00:00-05:00"}},
				{"attributes":{"status":"delivered","date":"2026-10-13T11:00:00-05:00"}}]}`)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(config.Config{SkydropxMode: "live", SkydropxBaseURL: srv.URL, SkydropxClientID: "id", SkydropxClientSecret: "s"})

	// By document: Ana's 3; the cousin's (another document, same phone) also matches by phone.
	badOnes, total, err := c.CustomerHistory(context.Background(), "111.111", "3001112233", 3)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("total = %d, se esperaban 4 envíos (3 de Ana y 1 con su teléfono)", total)
	}
	if len(badOnes) != 2 || badOnes[0].Kind != "no_pago" || badOnes[0].Tracking != "G1" ||
		badOnes[1].Kind != "oficina" || badOnes[1].Tracking != "G2" || badOnes[1].DaysAtOffice != 5 {
		t.Fatalf("antecedentes = %+v", badOnes)
	}
	// Only with another phone and the document: finds Ana's by document.
	if badOnes, _, _ := c.CustomerHistory(context.Background(), "111111", "3200000000", 3); len(badOnes) != 2 {
		t.Errorf("por cédula: %+v", badOnes)
	}
	// Someone without history.
	if badOnes, total, _ := c.CustomerHistory(context.Background(), "999999", "3150000000", 3); len(badOnes) != 0 || total != 0 {
		t.Errorf("sin historial: %+v %d", badOnes, total)
	}
}

func TestRecipientsFromList(t *testing.T) {
	list := ShipmentList{
		Data: []json.RawMessage{
			json.RawMessage(`{"id":"s1","attributes":{"created_at":"2026-11-01T10:00:00-05:00","master_tracking_number":"G1"},"relationships":{"address_to":{"data":{"id":"a1"}}}}`),
			json.RawMessage(`{"id":"s2","attributes":{"created_at":"2026-11-02T10:00:00-05:00","master_tracking_number":"G2","workflow_status":"cancelled"},"relationships":{"address_to":{"data":{"id":"a1"}}}}`),
			json.RawMessage(`{"id":"s3","attributes":{"created_at":"2026-11-03T10:00:00-05:00","master_tracking_number":""},"relationships":{"address_to":{"data":{"id":"a1"}}}}`),
		},
		Included: []json.RawMessage{
			json.RawMessage(`{"id":"a1","type":"address","attributes":{"name":" Ana Pérez ","phone":"3001112233","email":"ana@mail.com","tax_id_number":"111111","area_level1":"Córdoba","area_level2":"Montería","area_level3":"Centro","street1":"Calle 1 # 2-3"}}`),
		},
	}
	got := recipientsFromList(list)
	want := Recipient{Date: "2026-11-01T10:00:00-05:00", Name: "Ana Pérez", DocumentID: "111111", Phone: "3001112233",
		Email: "ana@mail.com", Department: "Córdoba", City: "Montería", Neighborhood: "Centro", Address: "Calle 1 # 2-3"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("solo cuenta el envío con guía y sin cancelar: %+v", got)
	}
}

func TestUndispatchedCandidates(t *testing.T) {
	list := ShipmentList{
		Data: []json.RawMessage{
			json.RawMessage(`{"id":"s2","attributes":{"created_at":"2026-11-14T17:50:00-05:00","master_tracking_number":"G2","carrier_name":"interrapidisimo"},"relationships":{"address_to":{"data":{"id":"a1"}}}}`),
			json.RawMessage(`{"id":"s1","attributes":{"created_at":"2026-11-13T18:10:00-05:00","master_tracking_number":"G1","carrier_name":"interrapidisimo"},"relationships":{"address_to":{"data":{"id":"a1"}}}}`),
			json.RawMessage(`{"id":"s3","attributes":{"created_at":"2026-11-14T10:00:00-05:00","master_tracking_number":"G3","workflow_status":"cancelled"}}`),
			json.RawMessage(`{"id":"s4","attributes":{"created_at":"2026-11-14T10:00:00-05:00","master_tracking_number":""}}`),
			json.RawMessage(`{"id":"s5","attributes":{"created_at":"2026-10-01T10:00:00-05:00","master_tracking_number":"G5"}}`),
		},
		Included: []json.RawMessage{
			json.RawMessage(`{"id":"a1","type":"address","attributes":{"name":"Ana Pérez","area_level1":"Córdoba","area_level2":"Montería"}}`),
		},
	}
	got := undispatchedCandidates(list, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if len(got) != 2 || got[0].Tracking != "G1" || got[1].Tracking != "G2" {
		t.Fatalf("labels in the window, not cancelled, oldest first: %+v", got)
	}
	if got[0].Name != "Ana Pérez" || got[0].City != "Montería" || got[0].Carrier != "interrapidisimo" {
		t.Fatalf("%+v", got[0])
	}
}
