package skydropx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrMockMode se devuelve cuando la app no está en live: no hay envíos reales que consultar.
var ErrMockMode = fmt.Errorf("modo mock: no hay envíos reales de Skydropx")

// get hace un GET autenticado con bearer token y devuelve el cuerpo y el status.
func (c *Client) get(ctx context.Context, u string) ([]byte, int, error) {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("error de conexión con Skydropx: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

// maxShipmentPages limita cuántas páginas se traen (20 envíos por página).
const maxShipmentPages = 15

// ShipmentList une todas las páginas de GET /api/v1/shipments.
type ShipmentList struct {
	Data      []json.RawMessage `json:"data"`
	Included  []json.RawMessage `json:"included"`
	Truncated bool              `json:"truncated"`
}

// ListShipments trae las páginas de GET /api/v1/shipments y las une en una sola lista.
func (c *Client) ListShipments(ctx context.Context) (ShipmentList, error) {
	var out ShipmentList
	if c.isMock() {
		return out, ErrMockMode
	}
	seenIncluded := map[string]bool{}
	for page := 1; ; page++ {
		u := fmt.Sprintf("%s/api/v1/shipments?page=%d&per_page=20", c.cfg.SkydropxBaseURL, page)
		body, status, err := c.get(ctx, u)
		if err != nil {
			return out, err
		}
		if status < 200 || status >= 300 {
			return out, fmt.Errorf("Skydropx respondió HTTP %d al listar envíos: %s", status, string(body))
		}
		var raw struct {
			Data     []json.RawMessage `json:"data"`
			Included []json.RawMessage `json:"included"`
			Meta     struct {
				TotalPages int `json:"total_pages"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return out, fmt.Errorf("respuesta inesperada de Skydropx al listar envíos: %w", err)
		}
		out.Data = append(out.Data, raw.Data...)
		for _, inc := range raw.Included {
			var key struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			_ = json.Unmarshal(inc, &key)
			k := key.Type + ":" + key.ID
			if !seenIncluded[k] {
				seenIncluded[k] = true
				out.Included = append(out.Included, inc)
			}
		}
		if len(raw.Data) == 0 || page >= raw.Meta.TotalPages {
			break
		}
		if page >= maxShipmentPages {
			out.Truncated = true
			break
		}
	}
	return out, nil
}

// GetShipment devuelve la respuesta completa de GET /api/v1/shipments/{id} (data + included), sin transformar.
func (c *Client) GetShipment(ctx context.Context, id string) (json.RawMessage, error) {
	if c.isMock() {
		return nil, ErrMockMode
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("ID de envío vacío")
	}
	body, status, err := c.get(ctx, c.cfg.SkydropxBaseURL+"/api/v1/shipments/"+url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Skydropx respondió HTTP %d al consultar el envío: %s", status, string(body))
	}
	return body, nil
}

// DownloadLabel descarga un PDF. Usa el token solo si la URL es del mismo host que la API.
func (c *Client) DownloadLabel(ctx context.Context, labelURL string) ([]byte, string, error) {
	if c.isMock() {
		return nil, "", ErrMockMode
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, labelURL, nil)
	if err != nil {
		return nil, "", err
	}
	if sameHost(labelURL, c.cfg.SkydropxBaseURL) {
		token, err := c.getAccessToken(ctx)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("error descargando la guía: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("Skydropx respondió HTTP %d al descargar la guía", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func sameHost(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && ua.Host == ub.Host
}

// Track consulta el rastreo de una guía (GET /api/v1/shipments/tracking) y devuelve la respuesta tal cual.
func (c *Client) Track(ctx context.Context, trackingNumber, carrierName string) (json.RawMessage, error) {
	if c.isMock() {
		return nil, ErrMockMode
	}
	q := url.Values{}
	q.Set("tracking_number", trackingNumber)
	q.Set("carrier_name", carrierName)
	body, status, err := c.get(ctx, c.cfg.SkydropxBaseURL+"/api/v1/shipments/tracking?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Skydropx respondió HTTP %d al rastrear la guía: %s", status, string(body))
	}
	return body, nil
}
