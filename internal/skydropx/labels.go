package skydropx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/parser"
)

// LabelCarrier es la transportadora con la que el bot genera las guías.
const LabelCarrier = "interrapidisimo"

const (
	// Si Skydropx no reconoce el código postal de la tabla, se prueban otros de la misma ciudad
	// (mismos 4 primeros dígitos, terminaciones 01 a maxPostalSuffix).
	maxPostalSuffix  = 20
	postalRetryPause = 600 * time.Millisecond // el límite de la API es de 2 solicitudes por segundo
)

// colombia es la hora de Colombia (UTC-5, sin horario de verano) para contar los días de las guías.
var colombia = time.FixedZone("COT", -5*3600)

// LabelQuote es la tarifa de Interrapidísimo lista para confirmar.
type LabelQuote struct {
	RateID     string  `json:"rate_id"`
	Total      float64 `json:"total"`
	Days       int     `json:"days"`
	Service    string  `json:"service"`
	PostalCode string  `json:"postal_code"` // código postal de destino que Skydropx aceptó
}

// QuoteLabel cotiza el envío solo con Interrapidísimo y devuelve la tarifa más barata.
// Si Skydropx no reconoce el código postal de destino, prueba otros códigos de la misma ciudad.
func (c *Client) QuoteLabel(ctx context.Context, data domain.ShipmentData) (LabelQuote, error) {
	if c.isMock() {
		return LabelQuote{}, ErrMockMode
	}
	originID, err := c.OriginTemplateID(ctx)
	if err != nil {
		return LabelQuote{}, err
	}
	postal := strings.TrimSpace(data.CodigoPostal)
	if postal == "" {
		postal = parser.LookupPostalCode(data.Ciudad)
	}
	if postal == "" {
		return LabelQuote{}, fmt.Errorf("no tengo código postal para %s. Vuelve a crear la orden con la línea \"Código postal: XXXXXX\"", data.Ciudad)
	}

	q, failMsg, err := c.quoteLabelWithPostal(ctx, data, originID, postal)
	if err != nil || failMsg == "" || !isUnknownPostal(failMsg) {
		return q, labelQuoteError(failMsg, err)
	}
	firstMsg := failMsg
	for _, alt := range postalCandidates(postal) {
		select {
		case <-ctx.Done():
			return LabelQuote{}, ctx.Err()
		case <-time.After(postalRetryPause):
		}
		q, failMsg, err = c.quoteLabelWithPostal(ctx, data, originID, alt)
		if err != nil || !isUnknownPostal(failMsg) {
			return q, labelQuoteError(failMsg, err)
		}
	}
	return LabelQuote{}, fmt.Errorf("Skydropx no reconoce el código postal %s de %s ni otros cercanos (%s). Vuelve a crear la orden con la línea \"Código postal: XXXXXX\"", postal, data.Ciudad, firstMsg)
}

func labelQuoteError(failMsg string, err error) error {
	if err != nil {
		return err
	}
	if failMsg != "" {
		return fmt.Errorf("%s", failMsg)
	}
	return nil
}

// quoteLabelWithPostal cotiza con un código postal concreto. failMsg explica por qué no hubo tarifa.
func (c *Client) quoteLabelWithPostal(ctx context.Context, data domain.ShipmentData, originID, postal string) (LabelQuote, string, error) {
	qty := maxInt(1, data.Cantidad)
	body := map[string]any{
		"quotation": map[string]any{
			// La dirección guardada rellena el origen: su código postal debe coincidir con el del envío.
			"address_from": map[string]any{
				"address_template_id": originID,
				"country_code":        c.cfg.OriginCountry,
			},
			"address_to": map[string]any{
				"country_code": "CO",
				"postal_code":  postal,
				"area_level1":  data.Departamento,
				"area_level2":  data.Ciudad,
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
			"requested_carriers":      []string{LabelCarrier},
		},
	}
	_, rates, failMsg, err := c.runQuotation(ctx, body)
	if err != nil || failMsg != "" {
		return LabelQuote{}, failMsg, err
	}
	q, ok, why := cheapestRate(rates, LabelCarrier)
	if !ok {
		return LabelQuote{}, why, nil
	}
	q.PostalCode = postal
	return q, "", nil
}

// cheapestRate elige la tarifa exitosa más barata de la transportadora.
func cheapestRate(raw []json.RawMessage, carrier string) (LabelQuote, bool, string) {
	var best LabelQuote
	found := false
	firstErr := ""
	for _, r := range raw {
		var rate struct {
			ID            string            `json:"id"`
			Success       bool              `json:"success"`
			ProviderName  string            `json:"provider_name"`
			ServiceName   string            `json:"provider_service_name"`
			Total         json.RawMessage   `json:"total"`
			Days          *int              `json:"days"`
			ErrorMessages []json.RawMessage `json:"error_messages"`
		}
		if json.Unmarshal(r, &rate) != nil || !strings.EqualFold(rate.ProviderName, carrier) {
			continue
		}
		if !rate.Success {
			if firstErr == "" {
				firstErr = errorText(rate.ErrorMessages)
			}
			continue
		}
		total := parseAmount(rate.Total)
		if !found || total < best.Total {
			best = LabelQuote{RateID: rate.ID, Total: total, Service: rate.ServiceName}
			if rate.Days != nil {
				best.Days = *rate.Days
			}
			found = true
		}
	}
	if found {
		return best, true, ""
	}
	if firstErr == "" {
		firstErr = "Interrapidísimo no tiene cobertura para esta dirección."
	}
	return LabelQuote{}, false, firstErr
}

// parseAmount acepta montos que llegan como número o como texto ("150.0").
func parseAmount(raw json.RawMessage) float64 {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// isUnknownPostal indica si Skydropx rechazó el código postal de destino por no existir.
func isUnknownPostal(msg string) bool {
	return strings.Contains(msg, "postal_code")
}

// postalCandidates devuelve otros códigos de la misma ciudad: 130001 -> 130002 … 130020.
func postalCandidates(postal string) []string {
	if len(postal) != 6 {
		return nil
	}
	var out []string
	for i := 1; i <= maxPostalSuffix; i++ {
		if alt := fmt.Sprintf("%s%02d", postal[:4], i); alt != postal {
			out = append(out, alt)
		}
	}
	return out
}

// CreateLabel crea el envío de la orden con la tarifa cotizada (POST /api/v1/shipments),
// sin Protección Plus. La guía la genera Skydropx después; devuelve el ID del envío.
func (c *Client) CreateLabel(ctx context.Context, orderID string, q LabelQuote, data domain.ShipmentData) (string, error) {
	if c.isMock() {
		return "", ErrMockMode
	}
	originID, err := c.OriginTemplateID(ctx)
	if err != nil {
		return "", err
	}
	email := strings.TrimSpace(data.Correo)
	if email == "" {
		email = c.cfg.OriginEmail // el correo del destinatario es obligatorio
	}
	reference := strings.TrimSpace(data.Barrio)
	if reference == "" {
		reference = "Sin referencia"
	}
	body := map[string]any{
		"shipment": map[string]any{
			"rate_id":         q.RateID,
			"order_id":        orderID,
			"unique_shipment": true, // si el botón se toca dos veces, Skydropx no crea otra guía con la misma tarifa
			"printing_format": "standard",
			"address_from": map[string]any{
				"address_template_id": originID,
				"street1":             c.cfg.OriginStreet,
				"name":                truncate(c.cfg.OriginName, 30),
				"company":             truncate(c.cfg.OriginCompany, 60),
				"phone":               c.cfg.OriginPhone,
				"email":               c.cfg.OriginEmail,
				"reference":           truncate(c.cfg.OriginReference, 30),
			},
			"address_to": map[string]any{
				"street1":       data.Direccion,
				"name":          truncate(data.Nombre, 30),
				"company":       truncate(data.Nombre, 60),
				"phone":         data.Telefono,
				"email":         email,
				"reference":     truncate(reference, 40),
				"tax_id_number": data.Documento,
			},
			"packages": []map[string]any{
				{
					"package_number":    "1",
					"package_protected": false, // sin Protección Plus
					"package_content":   c.cfg.DefaultContent,
					"package_type":      "4G",
				},
			},
		},
	}

	token, err := c.getAccessToken(ctx)
	if err != nil {
		return "", err
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.SkydropxBaseURL+"/api/v1/shipments/", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error de conexión con Skydropx al crear el envío: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Skydropx no creó el envío: %s", quotationErrorMessage(respBody, resp.StatusCode))
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil || created.Data.ID == "" {
		return "", fmt.Errorf("respuesta inesperada de Skydropx al crear el envío")
	}
	return created.Data.ID, nil
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}

// PrintLabel es una guía con PDF, lista para imprimir.
type PrintLabel struct {
	Tracking  string
	LabelURL  string
	CreatedAt time.Time
}

// RecentLabels devuelve las guías con PDF de los últimos days días contando hoy (hora de Colombia),
// de la más antigua a la más nueva. days = 1 es solo hoy.
func (c *Client) RecentLabels(ctx context.Context, days int) ([]PrintLabel, error) {
	list, err := c.ListShipments(ctx)
	if err != nil {
		return nil, err
	}
	return labelsSince(list, time.Now().In(colombia), days), nil
}

// labelsSince filtra los envíos con PDF creados desde el inicio del día de hace days-1 días
// (en la zona de now) hasta now.
func labelsSince(list ShipmentList, now time.Time, days int) []PrintLabel {
	labelByPackage := map[string]string{}
	for _, raw := range list.Included {
		var inc struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Attributes struct {
				LabelURL string `json:"label_url"`
			} `json:"attributes"`
		}
		if json.Unmarshal(raw, &inc) == nil && inc.Type == "package" && inc.Attributes.LabelURL != "" {
			labelByPackage[inc.ID] = inc.Attributes.LabelURL
		}
	}

	if days < 1 {
		days = 1
	}
	y, m, d := now.Date()
	from := time.Date(y, m, d-(days-1), 0, 0, 0, 0, now.Location())
	var out []PrintLabel
	for _, raw := range list.Data {
		var s struct {
			Attributes struct {
				CreatedAt string `json:"created_at"`
				Tracking  string `json:"master_tracking_number"`
			} `json:"attributes"`
			Relationships struct {
				Packages struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"packages"`
			} `json:"relationships"`
		}
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		created, ok := parseTime(s.Attributes.CreatedAt)
		if !ok {
			continue
		}
		created = created.In(now.Location())
		if created.Before(from) || created.After(now) {
			continue
		}
		for _, p := range s.Relationships.Packages.Data {
			if u := labelByPackage[p.ID]; u != "" {
				out = append(out, PrintLabel{Tracking: s.Attributes.Tracking, LabelURL: u, CreatedAt: created})
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// parseTime acepta las fechas de Skydropx con o sin zona horaria.
func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z07:00", "2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
