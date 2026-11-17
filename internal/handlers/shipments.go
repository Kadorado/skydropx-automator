package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"skydropx-automator/internal/skydropx"
)

// RegisterShipmentRoutes exposes Skydropx shipments for the "Mis envíos" tab.
func (h *APIHandler) RegisterShipmentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/skydropx/shipments", h.handleListShipments)
	mux.HandleFunc("GET /api/skydropx/shipments/{id}", h.handleShipmentDetail)
	mux.HandleFunc("GET /api/skydropx/shipments/{id}/label", h.handleShipmentLabel)
	mux.HandleFunc("GET /api/skydropx/tracking", h.handleTracking)
	mux.HandleFunc("GET /api/skydropx/status", h.handleStatuses)
	mux.HandleFunc("GET /api/skydropx/order-quantities", h.handleOrderQuantities)
	mux.HandleFunc("GET /api/finanzas", h.handleFinance)
	mux.HandleFunc("GET /api/ajustes", h.handleSettings)
	mux.HandleFunc("POST /api/ajustes/comision", h.handleSetCommission)
}

// handleSettings returns the seller's current commission and its changes.
func (h *APIHandler) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"comision":  h.settings.CurrentCommission(),
		"historial": h.settings.CommissionHistory(),
	})
}

// handleSetCommission changes the per-terminal commission from a date ({"valor":12000,"desde":"AAAA-MM-DD"};
// without desde, from today). Earlier sales keep their value.
func (h *APIHandler) handleSetCommission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Value float64 `json:"valor"`
		From  string  `json:"desde"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "datos inválidos")
		return
	}
	rate, err := h.settings.SetCommission(req.Value, req.From)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rate)
}

// handleFinance returns the money summary between ?desde= and ?hasta= (YYYY-MM-DD, Colombia time).
// Without desde, counts from the oldest available shipment; without hasta, up to today.
func (h *APIHandler) handleFinance(w http.ResponseWriter, r *http.Request) {
	period, err := skydropx.NewPeriod(r.URL.Query().Get("desde"), r.URL.Query().Get("hasta"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := h.skydropxClient.Finance(r.Context(), period, h.settings.CommissionAt)
	if err != nil {
		writeSkydropxError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// handleOrderQuantities returns the item count of the requested orders (?ids=a,b,c).
func (h *APIHandler) handleOrderQuantities(w http.ResponseWriter, r *http.Request) {
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	quantities, err := h.skydropxClient.OrderQuantities(r.Context(), ids)
	if err != nil {
		writeSkydropxError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quantities": quantities})
}

func writeSkydropxError(w http.ResponseWriter, err error) {
	if errors.Is(err, skydropx.ErrMockMode) {
		writeError(w, http.StatusConflict, "La app está en modo simulación. Cambia a SKYDROPX_MODE=live para ver envíos reales.")
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

func writeRawJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *APIHandler) handleListShipments(w http.ResponseWriter, r *http.Request) {
	list, err := h.skydropxClient.ListShipments(r.Context())
	if err != nil {
		writeSkydropxError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *APIHandler) handleShipmentDetail(w http.ResponseWriter, r *http.Request) {
	body, err := h.skydropxClient.GetShipment(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSkydropxError(w, err)
		return
	}
	writeRawJSON(w, body)
}

// handleShipmentLabel downloads the shipment's PDF through the server, so the token isn't exposed to the browser.
// ASSUMPTION: the label URL comes in data.attributes.label_url. The list doesn't include it today; verify with a finished shipment.
func (h *APIHandler) handleShipmentLabel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := h.skydropxClient.GetShipment(r.Context(), id)
	if err != nil {
		writeSkydropxError(w, err)
		return
	}

	// The label (label_url) comes in the shipment's package, inside "included" (type "package").
	var parsed struct {
		Included []struct {
			Type       string `json:"type"`
			Attributes struct {
				LabelURL string `json:"label_url"`
			} `json:"attributes"`
		} `json:"included"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeError(w, http.StatusBadGateway, "respuesta inesperada de Skydropx al consultar el envío")
		return
	}
	labelURL := ""
	for _, inc := range parsed.Included {
		if inc.Type == "package" && strings.TrimSpace(inc.Attributes.LabelURL) != "" {
			labelURL = strings.TrimSpace(inc.Attributes.LabelURL)
			break
		}
	}
	if labelURL == "" {
		writeError(w, http.StatusNotFound, "Este envío todavía no tiene guía disponible en la API.")
		return
	}

	data, contentType, err := h.skydropxClient.DownloadLabel(r.Context(), labelURL)
	if err != nil {
		writeSkydropxError(w, err)
		return
	}
	if contentType == "" || !strings.Contains(contentType, "pdf") {
		contentType = "application/pdf"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="guia-%s.pdf"`, id))
	_, _ = w.Write(data)
}

// handleTracking looks up a label's tracking (read only).
func (h *APIHandler) handleTracking(w http.ResponseWriter, r *http.Request) {
	tn := strings.TrimSpace(r.URL.Query().Get("tracking_number"))
	carrier := strings.TrimSpace(r.URL.Query().Get("carrier_name"))
	if tn == "" || carrier == "" {
		writeError(w, http.StatusBadRequest, "Faltan tracking_number o carrier_name")
		return
	}
	body, err := h.skydropxClient.Track(r.Context(), tn, carrier)
	if err != nil {
		writeSkydropxError(w, err)
		return
	}
	writeRawJSON(w, body)
}

// handleStatuses returns the logistics and collection state of each shipment (cached).
func (h *APIHandler) handleStatuses(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.skydropxClient.ShipmentStatuses(r.Context())
	if err != nil {
		writeSkydropxError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"statuses": statuses})
}
