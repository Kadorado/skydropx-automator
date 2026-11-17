package handlers

import (
	"encoding/json"
	"net/http"

	"skydropx-automator/internal/domain"
)

// handleValidateAddress checks address coverage before creating the order ("Validar dirección" button).
func (h *APIHandler) handleValidateAddress(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateShipmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Datos de dirección inválidos")
		return
	}
	result, err := h.skydropxClient.ValidateCoverage(r.Context(), req.Data)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
