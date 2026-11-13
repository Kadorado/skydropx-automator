package skydropx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/parser"
)

// CoverageResult es el resultado de validar una dirección cotizando la ruta en Skydropx.
type CoverageResult struct {
	OK       bool     `json:"ok"`
	Carriers []string `json:"carriers"`
	Message  string   `json:"message"`
}

const (
	quotePollAttempts = 10
	quotePollInterval = 1 * time.Second // el límite de la API es de 2 solicitudes por segundo
)

// ValidateCoverage cotiza la ruta origen -> destino (POST /api/v1/quotations) y revisa las tarifas.
// Si ninguna transportadora cubre la dirección, OK es false y Message explica por qué.
func (c *Client) ValidateCoverage(ctx context.Context, data domain.ShipmentData) (CoverageResult, error) {
	if c.isMock() {
		return CoverageResult{OK: true, Message: "Modo simulación: validación de cobertura omitida."}, nil
	}

	destPostal := strings.TrimSpace(data.CodigoPostal)
	if destPostal == "" {
		destPostal = parser.LookupPostalCode(data.Ciudad)
	}
	if destPostal == "" {
		// Sin código postal no se puede cotizar: se omite la validación en vez de bloquear la orden.
		return CoverageResult{OK: true, Message: "Sin código postal de destino: validación de cobertura omitida."}, nil
	}

	qty := maxInt(1, data.Cantidad)
	body := map[string]any{
		"quotation": map[string]any{
			"address_from": map[string]any{
				"country_code": c.cfg.OriginCountry,
				"postal_code":  c.cfg.OriginPostalCode,
				"area_level1":  c.cfg.OriginState,
				"area_level2":  c.cfg.OriginCity,
				"street1":      c.cfg.OriginStreet,
			},
			"address_to": map[string]any{
				"country_code": "CO",
				"postal_code":  destPostal,
				"area_level1":  data.Departamento,
				"area_level2":  data.Ciudad,
				"street1":      data.Direccion,
			},
			"parcels": []map[string]any{
				{
					"length":          c.cfg.DefaultLengthCm,
					"width":           c.cfg.DefaultWidthCm,
					"height":          c.cfg.DefaultHeightCm,
					"weight":          c.cfg.DefaultWeightKg * float64(qty),
					"declared_amount": data.ValorUnitario * float64(qty),
				},
			},
			"cash_on_delivery":        true,
			"recipient_pays_shipping": true,
		},
	}

	token, err := c.getAccessToken(ctx)
	if err != nil {
		return CoverageResult{}, err
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.SkydropxBaseURL+"/api/v1/quotations", bytes.NewReader(bodyBytes))
	if err != nil {
		return CoverageResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CoverageResult{}, fmt.Errorf("error de conexión con Skydropx al cotizar: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CoverageResult{OK: false, Message: quotationErrorMessage(respBody, resp.StatusCode)}, nil
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil || created.ID == "" {
		return CoverageResult{}, fmt.Errorf("respuesta inesperada de Skydropx al cotizar")
	}

	// Espera a que la cotización termine de cargar las tarifas (is_completed).
	for i := 0; i < quotePollAttempts; i++ {
		select {
		case <-ctx.Done():
			return CoverageResult{}, ctx.Err()
		case <-time.After(quotePollInterval):
		}
		qBody, status, err := c.get(ctx, c.cfg.SkydropxBaseURL+"/api/v1/quotations/"+created.ID)
		if err != nil {
			return CoverageResult{}, err
		}
		if status < 200 || status >= 300 {
			return CoverageResult{OK: false, Message: quotationErrorMessage(qBody, status)}, nil
		}
		var q struct {
			IsCompleted bool              `json:"is_completed"`
			Rates       []json.RawMessage `json:"rates"`
		}
		if err := json.Unmarshal(qBody, &q); err != nil {
			return CoverageResult{}, fmt.Errorf("respuesta inesperada de Skydropx al consultar la cotización")
		}
		if q.IsCompleted {
			return evaluateRates(q.Rates), nil
		}
	}
	return CoverageResult{OK: false, Message: "Skydropx tardó demasiado en validar la dirección. Intenta de nuevo."}, nil
}

// evaluateRates decide si hay al menos una tarifa válida y arma el mensaje.
func evaluateRates(raw []json.RawMessage) CoverageResult {
	var carriers []string
	var firstErr string
	for _, r := range raw {
		var rate struct {
			Success             bool              `json:"success"`
			ProviderDisplayName string            `json:"provider_display_name"`
			ProviderServiceName string            `json:"provider_service_name"`
			ErrorMessages       []json.RawMessage `json:"error_messages"`
		}
		if err := json.Unmarshal(r, &rate); err != nil {
			continue
		}
		if rate.Success {
			name := strings.TrimSpace(rate.ProviderDisplayName + " " + rate.ProviderServiceName)
			if name != "" {
				carriers = append(carriers, name)
			}
			continue
		}
		if firstErr == "" {
			firstErr = errorText(rate.ErrorMessages)
		}
	}

	if len(carriers) > 0 {
		return CoverageResult{OK: true, Carriers: carriers, Message: fmt.Sprintf("Dirección válida: %d tarifa(s) disponible(s).", len(carriers))}
	}
	if firstErr == "" {
		firstErr = "Skydropx no encontró transportadora con cobertura para esta dirección."
	}
	return CoverageResult{OK: false, Message: firstErr}
}

// errorText extrae el mensaje legible de error_messages (acepta texto u objeto con display_message).
func errorText(msgs []json.RawMessage) string {
	for _, m := range msgs {
		var s string
		if json.Unmarshal(m, &s) == nil && strings.TrimSpace(s) != "" && s != "string" {
			return s
		}
		var obj struct {
			DisplayMessage string `json:"display_message"`
			ErrorMessage   string `json:"error_message"`
		}
		if json.Unmarshal(m, &obj) == nil {
			if obj.DisplayMessage != "" {
				return obj.DisplayMessage
			}
			if obj.ErrorMessage != "" {
				return obj.ErrorMessage
			}
		}
	}
	return ""
}

// formatErrors convierte {"campo": ["mensaje"]} en "campo: mensaje".
func formatErrors(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
	var parts []string
	for field, val := range m {
		var msgs []string
		switch t := val.(type) {
		case []any:
			for _, x := range t {
				msgs = append(msgs, fmt.Sprintf("%v", x))
			}
		default:
			msgs = append(msgs, fmt.Sprintf("%v", t))
		}
		parts = append(parts, fmt.Sprintf("%s: %s", field, strings.Join(msgs, ", ")))
	}
	return strings.Join(parts, " · ")
}

func quotationErrorMessage(body []byte, status int) string {
	var generic struct {
		Error        string `json:"error"`
		ErrorMessage string `json:"error_message"`
		Errors       any    `json:"errors"`
	}
	if json.Unmarshal(body, &generic) == nil {
		if generic.Error != "" {
			return generic.Error
		}
		if generic.ErrorMessage != "" {
			return generic.ErrorMessage
		}
		if msg := formatErrors(generic.Errors); msg != "" {
			return msg
		}
	}
	return fmt.Sprintf("Skydropx respondió HTTP %d al validar la dirección.", status)
}
