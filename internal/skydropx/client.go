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

	// La lista de envíos se comparte unos segundos entre el panel, el rastreo, finanzas y el bot.
	listMu       sync.Mutex
	listCache    ShipmentList
	listCachedAt time.Time
}

func NewClient(cfg config.Config) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 25 * time.Second,
		},
	}
}

// CreateShipment registra el pedido en Skydropx como orden (sin emitir guía),
// o responde en modo mock si no hay modo live + credenciales OAuth.
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
		RecipientName:  data.Nombre,
		Destination:    fmt.Sprintf("%s, %s", data.Ciudad, data.Departamento),
		Carrier:        c.cfg.PreferredCarrier,
		TrackingNumber: tracking,
		LabelURL:       fmt.Sprintf("/api/mock-label/%s?name=%s&city=%s", tracking, urlEscape(data.Nombre), urlEscape(data.Ciudad)),
		EstimatedCost:  "$14.800 COP (Simulado)",
		IsMock:         true,
		CreatedAt:      now,
	}
}

// getAccessToken obtiene (y cachea) el bearer token OAuth. Expira en 2 horas según la documentación.
// ASUNCIÓN: el body de POST /api/v1/oauth/token usa grant_type=client_credentials con client_id/client_secret
// y la respuesta trae access_token y expires_in. Verificar contra el OpenAPI de la cuenta.
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
	c.tokenExpiry = time.Now().Add(ttl - 2*time.Minute) // margen antes de expirar
	return c.accessToken, nil
}

// createLiveOrder crea una orden en Skydropx (POST /api/v1/orders/) con origen y destinatario.
// No cotiza ni emite guía: la orden queda lista para que se finalice a mano en Skydropx.
func (c *Client) createLiveOrder(ctx context.Context, data domain.ShipmentData) (domain.ShipmentResult, error) {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return domain.ShipmentResult{}, err
	}

	// La dirección guardada en Skydropx reemplaza los campos de origen enviados abajo.
	originTemplateID, err := c.OriginTemplateID(ctx)
	if err != nil {
		return domain.ShipmentResult{}, err
	}

	destPostal := strings.TrimSpace(data.CodigoPostal)
	if destPostal == "" {
		destPostal = parser.LookupPostalCode(data.Ciudad)
	}

	qty := maxInt(1, data.Cantidad)
	declaredAmount := data.ValorUnitario * float64(qty) // valor declarado = unitario × unidades
	now := time.Now()
	reference := fmt.Sprintf("WA-%d", now.UnixMilli())

	// ASUNCIÓN: el campo tax_id_number del destinatario recibe el documento de identidad.
	order := map[string]any{
		"reference":               reference,
		"cash_on_delivery":        true, // contra entrega: la transportadora recauda el valor declarado
		"recipient_pays_shipping": true, // el destinatario paga el costo del envío al recibir
		"parcels": []map[string]any{
			{
				"weight":            c.cfg.DefaultWeightKg * float64(qty),
				"length":            c.cfg.DefaultLengthCm,
				"width":             c.cfg.DefaultWidthCm,
				"height":            c.cfg.DefaultHeightCm,
				"quantity":          1,
				"dimension_unit":    "CM",
				"mass_unit":         "KG",
				"package_type":      "4G", // ASUNCIÓN: código de empaque por defecto; confirmar con la lista de códigos
				"package_content":   c.cfg.DefaultContent,
				"declared_amount":   declaredAmount,
				"package_protected": false, // Protección Plus desactivada
				"products": []map[string]any{
					{
						"name":     c.cfg.DefaultContent,
						"price":    fmt.Sprintf("%.2f", data.ValorUnitario),
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
			"address":       data.Direccion,
			"sector":        data.Barrio,
			"reference":     data.Referencia,
			"city":          data.Ciudad,
			"state":         data.Departamento,
			"postal_code":   destPostal,
			"country":       "CO",
			"person_name":   data.Nombre,
			"phone":         data.Telefono,
			"email":         data.Correo,
			"tax_id_number": data.Documento,
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
		Status:        "pending_label", // orden creada; la guía se emite manualmente en Skydropx
		RecipientName: data.Nombre,
		Destination:   fmt.Sprintf("%s, %s", data.Ciudad, data.Departamento),
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
