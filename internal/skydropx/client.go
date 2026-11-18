package skydropx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"skydropx-automator/internal/config"
	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/parser"
)

type Client struct {
	cfg        config.Config
	httpClient *http.Client

	tokenMu     sync.Mutex
	accessToken string
	tokenExpiry time.Time

	origin originTemplate

	// The shipment list is shared for a few seconds between the dashboard, tracking, finance and the bot.
	listMu       sync.Mutex
	listCache    ShipmentList
	listCachedAt time.Time
}

func NewClient(cfg config.Config) *Client {
	c := &Client{cfg: cfg}
	c.httpClient = &http.Client{
		Timeout:   25 * time.Second,
		Transport: &tokenRetry{c: c, base: http.DefaultTransport},
	}
	return c
}

// tokenRetry retries once with a new token when Skydropx answers 401: the token can stop working
// some minutes before the expiry it announced.
type tokenRetry struct {
	c    *Client
	base http.RoundTripper
}

func (t *tokenRetry) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	auth := req.Header.Get("Authorization")
	if err != nil || resp.StatusCode != http.StatusUnauthorized || auth == "" || (req.Body != nil && req.GetBody == nil) {
		return resp, err
	}
	t.c.dropToken(strings.TrimPrefix(auth, "Bearer "))
	token, tokErr := t.c.getAccessToken(req.Context())
	if tokErr != nil {
		return resp, nil // keep the original 401
	}
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		if retry.Body, err = req.GetBody(); err != nil {
			return resp, nil
		}
	}
	retry.Header.Set("Authorization", "Bearer "+token)
	resp.Body.Close()
	return t.base.RoundTrip(retry)
}

// dropToken forgets the cached token if it is still the one that failed.
func (c *Client) dropToken(failed string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.accessToken == failed {
		c.accessToken = ""
	}
}

// CreateShipment registers the order in Skydropx as an order (without issuing a label),
// or answers in mock mode if there is no live mode + OAuth credentials.
func (c *Client) CreateShipment(ctx context.Context, data domain.ShipmentData) (domain.ShipmentResult, error) {
	if c.isMock() {
		return c.createMockShipment(data), nil
	}
	return c.createLiveOrder(ctx, data)
}

func (c *Client) isMock() bool {
	return c.cfg.SkydropxMode == "mock" || c.cfg.SkydropxClientID == "" || c.cfg.SkydropxClientSecret == ""
}

func (c *Client) createMockShipment(data domain.ShipmentData) domain.ShipmentResult {
	now := time.Now()
	tracking := fmt.Sprintf("7000%08d", now.UnixNano()%100000000)
	shipmentID := fmt.Sprintf("shp_mock_%d", now.Unix())

	return domain.ShipmentResult{
		ID:             shipmentID,
		Status:         "created",
		RecipientName:  data.Name,
		Destination:    fmt.Sprintf("%s, %s", data.City, data.Department),
		Carrier:        c.cfg.PreferredCarrier,
		TrackingNumber: tracking,
		LabelURL:       fmt.Sprintf("/api/mock-label/%s?name=%s&city=%s", tracking, urlEscape(data.Name), urlEscape(data.City)),
		EstimatedCost:  "$14.800 COP (Simulado)",
		IsMock:         true,
		CreatedAt:      now,
	}
}

// getAccessToken gets (and caches) the OAuth bearer token. It expires in 2 hours according to the docs.
// ASSUMPTION: the body of POST /api/v1/oauth/token uses grant_type=client_credentials with client_id/client_secret
// and the response has access_token and expires_in. Verify against the account's OpenAPI.
func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	body, _ := json.Marshal(map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     c.cfg.SkydropxClientID,
		"client_secret": c.cfg.SkydropxClientSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.SkydropxBaseURL+"/api/v1/oauth/token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error de conexión con Skydropx (OAuth): %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Skydropx rechazó las credenciales OAuth (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("respuesta OAuth inesperada de Skydropx")
	}

	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	c.accessToken = tok.AccessToken
	c.tokenExpiry = time.Now().Add(ttl - 2*time.Minute) // margin before expiry
	return c.accessToken, nil
}

// createLiveOrder creates an order in Skydropx (POST /api/v1/orders/) with origin and recipient.
// It neither quotes nor issues a label: the order is ready to be finished by hand in Skydropx.
func (c *Client) createLiveOrder(ctx context.Context, data domain.ShipmentData) (domain.ShipmentResult, error) {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return domain.ShipmentResult{}, err
	}

	// The address saved in Skydropx replaces the origin fields sent below.
	originTemplateID, err := c.OriginTemplateID(ctx)
	if err != nil {
		return domain.ShipmentResult{}, err
	}

	destPostal := strings.TrimSpace(data.PostalCode)
	if destPostal == "" {
		destPostal = parser.LookupPostalCode(data.City, data.Department)
	}

	qty := maxInt(1, data.Quantity)
	declaredAmount := data.UnitPrice * float64(qty) // valor declarado = unitario × unidades
	now := time.Now()
	reference := fmt.Sprintf("WA-%d", now.UnixMilli())

	// ASSUMPTION: the recipient's tax_id_number field takes the ID document.
	order := map[string]any{
		"reference":               reference,
		"cash_on_delivery":        true, // cash on delivery: the carrier collects the declared value
		"recipient_pays_shipping": true, // the recipient pays the shipping cost on receipt
		"parcels": []map[string]any{
			{
				"weight":            c.cfg.DefaultWeightKg * float64(qty),
				"length":            c.cfg.DefaultLengthCm,
				"width":             c.cfg.DefaultWidthCm,
				"height":            c.cfg.DefaultHeightCm,
				"quantity":          1,
				"dimension_unit":    "CM",
				"mass_unit":         "KG",
				"package_type":      "4G", // ASSUMPTION: default packaging code; confirm with the code list
				"package_content":   c.cfg.DefaultContent,
				"declared_amount":   declaredAmount,
				"package_protected": false, // Protección Plus disabled
				"products": []map[string]any{
					{
						"name":     c.cfg.DefaultContent,
						"price":    fmt.Sprintf("%.2f", data.UnitPrice),
						"quantity": qty,
						"weight":   c.cfg.DefaultWeightKg,
						"height":   c.cfg.DefaultHeightCm,
						"length":   c.cfg.DefaultLengthCm,
						"width":    c.cfg.DefaultWidthCm,
					},
				},
			},
		},
		"shipper_address": map[string]any{
			"address_template_id": originTemplateID,
			"address":             c.cfg.OriginStreet,
			"internal_number":     c.cfg.OriginInternalNumber,
			"reference":           c.cfg.OriginReference,
			"sector":              c.cfg.OriginNeighborhood,
			"city":                c.cfg.OriginCity,
			"state":               c.cfg.OriginState,
			"postal_code":         c.cfg.OriginPostalCode,
			"country":             c.cfg.OriginCountry,
			"person_name":         c.cfg.OriginName,
			"company":             c.cfg.OriginCompany,
			"phone":               c.cfg.OriginPhone,
			"email":               c.cfg.OriginEmail,
		},
		"recipient_address": map[string]any{
			"address":       data.Address,
			"sector":        data.Neighborhood,
			"reference":     data.Reference,
			"city":          data.City,
			"state":         data.Department,
			"postal_code":   destPostal,
			"country":       "CO",
			"person_name":   data.Name,
			"phone":         data.Phone,
			"email":         data.Email,
			"tax_id_number": data.Document,
		},
	}

	bodyBytes, err := json.Marshal(map[string]any{"order": order})
	if err != nil {
		return domain.ShipmentResult{}, fmt.Errorf("error serializando orden: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.SkydropxBaseURL+"/api/v1/orders/", bytes.NewReader(bodyBytes))
	if err != nil {
		return domain.ShipmentResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return domain.ShipmentResult{}, fmt.Errorf("error de conexión con Skydropx: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domain.ShipmentResult{}, fmt.Errorf("Skydropx respondió HTTP %d al crear la orden: %s", resp.StatusCode, string(respBody))
	}

	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil || created.Data.ID == "" {
		return domain.ShipmentResult{}, fmt.Errorf("respuesta inesperada de Skydropx al crear la orden")
	}

	return domain.ShipmentResult{
		ID:            created.Data.ID,
		Status:        "pending_label", // order created; the label is issued manually in Skydropx
		RecipientName: data.Name,
		Destination:   fmt.Sprintf("%s, %s", data.City, data.Department),
		Carrier:       c.cfg.PreferredCarrier,
		IsMock:        false,
		CreatedAt:     now,
	}, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func urlEscape(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}
