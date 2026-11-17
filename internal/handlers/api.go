package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"skydropx-automator/internal/config"
	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/parser"
	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
)

type idempotencyEntry struct {
	Result    domain.ShipmentResult
	ExpiresAt time.Time
}

type APIHandler struct {
	cfg            config.Config
	skydropxClient *skydropx.Client
	settings       *settings.Store

	mu          sync.Mutex
	idempotency map[string]idempotencyEntry
	shipments   map[string]domain.ShipmentResult
}

func NewAPIHandler(cfg config.Config, client *skydropx.Client, st *settings.Store) *APIHandler {
	return &APIHandler{
		cfg:            cfg,
		skydropxClient: client,
		settings:       st,
		idempotency:    make(map[string]idempotencyEntry),
		shipments:      make(map[string]domain.ShipmentResult),
	}
}

func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", h.handleHealth)
	mux.HandleFunc("POST /api/messages/parse", h.handleParseMessage)
	mux.HandleFunc("POST /api/shipments", h.handleCreateShipment)
	mux.HandleFunc("GET /api/shipments/{id}", h.handleGetShipment)
	mux.HandleFunc("GET /api/mock-label/{tracking}", h.handleMockLabel)
	h.RegisterShipmentRoutes(mux)
	mux.HandleFunc("POST /api/validate-address", h.handleValidateAddress)
}

func (h *APIHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"mode":    h.cfg.SkydropxMode,
		"origin":  fmt.Sprintf("%s, %s", h.cfg.OriginCity, h.cfg.OriginState),
		"carrier": h.cfg.PreferredCarrier,
	})
}

func (h *APIHandler) handleParseMessage(w http.ResponseWriter, r *http.Request) {
	var req domain.ParseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Formato de solicitud inválido")
		return
	}

	if strings.TrimSpace(req.RawMessage) == "" {
		writeError(w, http.StatusBadRequest, "El mensaje de WhatsApp no puede estar vacío")
		return
	}

	parsed := parser.ParseWhatsAppMessage(req.RawMessage)
	writeJSON(w, http.StatusOK, parsed)
}

func (h *APIHandler) handleCreateShipment(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateShipmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Datos de envío inválidos")
		return
	}

	// 1. Validate required fields before contacting Skydropx
	missing := parser.ValidateShipmentData(req.Data)
	if len(missing) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":          "Faltan campos obligatorios o tienen formato inválido",
			"missing_fields": missing,
		})
		return
	}

	// 2. Idempotency check to avoid duplicate shipments from double clicks or retries
	idemKey := strings.TrimSpace(req.IdempotencyKey)
	if idemKey == "" {
		idemKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}

	if idemKey != "" {
		h.mu.Lock()
		h.cleanupExpiredLocked()
		if entry, exists := h.idempotency[idemKey]; exists {
			h.mu.Unlock()
			writeJSON(w, http.StatusOK, entry.Result)
			return
		}
		h.mu.Unlock()
	}

	// 3. Create the shipment in Skydropx
	result, err := h.skydropxClient.CreateShipment(r.Context(), req.Data)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// 4. Save in the temporary cache (idempotency and lookup by ID)
	h.mu.Lock()
	if idemKey != "" {
		h.idempotency[idemKey] = idempotencyEntry{
			Result:    result,
			ExpiresAt: time.Now().Add(30 * time.Minute),
		}
	}
	h.shipments[result.ID] = result
	h.mu.Unlock()

	writeJSON(w, http.StatusCreated, result)
}

func (h *APIHandler) handleGetShipment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.mu.Lock()
	res, ok := h.shipments[id]
	h.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "Envío no encontrado en la sesión actual")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleMockLabel renders a simulated printable view when SKYDROPX_MODE=mock
func (h *APIHandler) handleMockLabel(w http.ResponseWriter, r *http.Request) {
	tracking := r.PathValue("tracking")
	name := r.URL.Query().Get("name")
	city := r.URL.Query().Get("city")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="es">
<head><meta charset="UTF-8"><title>Guía Simulada %s</title>
<style>
body{font-family:monospace;padding:30px;max-width:450px;margin:auto;}
.box{border:2px solid #000;padding:20px;}
h2{margin-top:0;border-bottom:2px solid #000;padding-bottom:8px;}
.barcode{background:#000;color:#fff;text-align:center;padding:12px;font-size:18px;letter-spacing:4px;margin:16px 0;}
</style></head>
<body>
<div class="box">
<h2>INTERRAPIDÍSIMO (MODO PRUEBA)</h2>
<p><strong>ORIGEN:</strong> %s, %s</p>
<p><strong>DESTINATARIO:</strong> %s</p>
<p><strong>DESTINO:</strong> %s</p>
<div class="barcode">*%s*</div>
<p style="font-size:12px;">Documento generado en modo simulación (SKYDROPX_MODE=mock). Configure su API Key real para obtener el PDF oficial.</p>
<button onclick="window.print()">Imprimir Guía</button>
</div>
</body></html>`, tracking, h.cfg.OriginCity, h.cfg.OriginState, name, city, tracking)
}

func (h *APIHandler) cleanupExpiredLocked() {
	now := time.Now()
	for k, v := range h.idempotency {
		if now.After(v.ExpiresAt) {
			delete(h.idempotency, k)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
