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

// LabelCarrier is the carrier the bot generates labels with.
const LabelCarrier = "interrapidisimo"

const (
	// If Skydropx doesn't recognize the postal code from the table, others from the same city are tried
	// (same first 4 digits, endings 01 to maxPostalSuffix).
	maxPostalSuffix  = 20
	postalRetryPause = 600 * time.Millisecond // the API limit is 2 requests per second
)

// colombia is Colombia time (UTC-5, no daylight saving) for counting label days.
var colombia = time.FixedZone("COT", -5*3600)

// LabelQuote is the Interrapidísimo rate ready to confirm.
type LabelQuote struct {
	RateID     string  `json:"rate_id"`
	Total      float64 `json:"total"`
	Days       int     `json:"days"`
	Service    string  `json:"service"`
	PostalCode string  `json:"postal_code"` // destination postal code Skydropx accepted
}

// QuoteLabel quotes the shipment only with Interrapidísimo and returns the cheapest rate.
// If Skydropx doesn't recognize the destination postal code, it tries the municipality's other codes
// and then nearby numbers.
func (c *Client) QuoteLabel(ctx context.Context, data domain.ShipmentData) (LabelQuote, error) {
	if c.isMock() {
		return LabelQuote{}, ErrMockMode
	}
	originID, err := c.OriginTemplateID(ctx)
	if err != nil {
		return LabelQuote{}, err
	}
	postal := strings.TrimSpace(data.PostalCode)
	if postal == "" {
		postal = parser.LookupPostalCode(data.City, data.Department)
	}
	if postal == "" {
		return LabelQuote{}, fmt.Errorf("no tengo código postal para %s. Vuelve a crear la orden con la línea \"Código postal: XXXXXX\"", data.City)
	}

	q, failMsg, err := c.quoteLabelWithPostal(ctx, data, originID, postal)
	if err != nil || failMsg == "" || !isUnknownPostal(failMsg) {
		return q, labelQuoteError(failMsg, err)
	}
	firstMsg := failMsg
	for _, alt := range retryPostals(postal, parser.OtherPostalCodes(data.City, data.Department)) {
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
	return LabelQuote{}, fmt.Errorf("Skydropx no reconoce el código postal %s de %s ni otros cercanos (%s). Vuelve a crear la orden con la línea \"Código postal: XXXXXX\"", postal, data.City, firstMsg)
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

// quoteLabelWithPostal quotes with a specific postal code. failMsg explains why there was no rate.
func (c *Client) quoteLabelWithPostal(ctx context.Context, data domain.ShipmentData, originID, postal string) (LabelQuote, string, error) {
	qty := maxInt(1, data.Quantity)
	body := map[string]any{
		"quotation": map[string]any{
			// The saved address fills the origin: its postal code must match the shipment's.
			"address_from": map[string]any{
				"address_template_id": originID,
				"country_code":        c.cfg.OriginCountry,
			},
			"address_to": map[string]any{
				"country_code": "CO",
				"postal_code":  postal,
				"area_level1":  data.Department,
				"area_level2":  data.City,
			},
			"parcels": []map[string]any{
				{
					"length":          c.cfg.DefaultLengthCm,
					"width":           c.cfg.DefaultWidthCm,
					"height":          c.cfg.DefaultHeightCm,
					"weight":          c.cfg.DefaultWeightKg * float64(qty),
					"declared_amount": data.UnitPrice * float64(qty),
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

// cheapestRate picks the carrier's cheapest successful rate.
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

// parseAmount accepts amounts that come as a number or as text ("150.0").
func parseAmount(raw json.RawMessage) float64 {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// isUnknownPostal reports whether Skydropx rejected the destination postal code as nonexistent.
func isUnknownPostal(msg string) bool {
	return strings.Contains(msg, "postal_code")
}

// retryPostals lists the codes to try after postal: first the municipality's own 4-72 codes, then
// the neighboring numbers (which may belong to another municipality).
func retryPostals(postal string, own []string) []string {
	seen := map[string]bool{postal: true}
	var out []string
	for _, c := range append(append([]string(nil), own...), postalCandidates(postal)...) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// postalCandidates returns other codes from the same city: 130001 -> 130002 … 130020.
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

// CreateLabel creates the order's shipment with the quoted rate (POST /api/v1/shipments),
// without Protección Plus. Skydropx generates the label afterwards; it returns the shipment ID.
func (c *Client) CreateLabel(ctx context.Context, orderID string, q LabelQuote, data domain.ShipmentData) (string, error) {
	if c.isMock() {
		return "", ErrMockMode
	}
	originID, err := c.OriginTemplateID(ctx)
	if err != nil {
		return "", err
	}
	email := strings.TrimSpace(data.Email)
	if email == "" {
		email = c.cfg.OriginEmail // the recipient's email is required
	}
	reference := strings.TrimSpace(data.Neighborhood)
	if reference == "" {
		reference = "Sin referencia"
	}
	body := map[string]any{
		"shipment": map[string]any{
			"rate_id":         q.RateID,
			"order_id":        orderID,
			"unique_shipment": true, // if the button is tapped twice, Skydropx doesn't create another label with the same rate
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
				"street1":       data.Address,
				"name":          truncate(data.Name, 30),
				"company":       truncate(data.Name, 60),
				"phone":         data.Phone,
				"email":         email,
				"reference":     truncate(reference, 40),
				"tax_id_number": data.Document,
			},
			"packages": []map[string]any{
				{
					"package_number":    "1",
					"package_protected": false, // without Protección Plus
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
	c.forgetShipmentList() // the new label must show up in the next lookup
	return created.Data.ID, nil
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}

// PrintLabel is a label with a PDF, ready to print.
type PrintLabel struct {
	Tracking  string
	LabelURL  string
	CreatedAt time.Time
}

// RecentLabels returns the labels with a PDF from the last days days including today (Colombia time),
// oldest first. days = 1 is today only.
func (c *Client) RecentLabels(ctx context.Context, days int) ([]PrintLabel, error) {
	list, err := c.ListShipments(ctx)
	if err != nil {
		return nil, err
	}
	return labelsSince(list, time.Now().In(colombia), days), nil
}

// labelsSince filters the shipments with a PDF created from the start of the day days-1 days ago
// (in now's zone) until now.
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

// parseTime accepts Skydropx dates with or without a time zone.
func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z07:00", "2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
