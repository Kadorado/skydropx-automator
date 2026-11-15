package skydropx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrMockMode se devuelve cuando la app no está en live: no hay envíos reales que consultar.
var ErrMockMode = fmt.Errorf("modo mock: no hay envíos reales de Skydropx")

const (
	shipmentListTTL     = 30 * time.Second       // cuánto se reutiliza la lista de envíos ya descargada
	shipmentPagePause   = 600 * time.Millisecond // el límite de la API es de 2 solicitudes por segundo
	maxRateLimitRetries = 3                      // reintentos cuando Skydropx responde 429
)

// get hace un GET autenticado y, si Skydropx responde 429 (límite de solicitudes), espera y reintenta.
func (c *Client) get(ctx context.Context, u string) ([]byte, int, error) {
	for attempt := 0; ; attempt++ {
		body, status, err := c.getOnce(ctx, u)
		if err != nil || status != http.StatusTooManyRequests || attempt >= maxRateLimitRetries {
			return body, status, err
		}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		}
	}
}

// getOnce hace un GET autenticado con bearer token y devuelve el cuerpo y el status.
func (c *Client) getOnce(ctx context.Context, u string) ([]byte, int, error) {
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
// Si se pide varias veces seguidas (panel, rastreo, finanzas, bot), reutiliza la misma descarga
// durante shipmentListTTL; las llamadas simultáneas esperan a la primera en vez de repetirla.
func (c *Client) ListShipments(ctx context.Context) (ShipmentList, error) {
	if c.isMock() {
		return ShipmentList{}, ErrMockMode
	}
	c.listMu.Lock()
	defer c.listMu.Unlock()
	if !c.listCachedAt.IsZero() && time.Since(c.listCachedAt) < shipmentListTTL {
		return c.listCache, nil
	}
	out, err := c.fetchShipments(ctx)
	if err != nil {
		return out, err
	}
	c.listCache, c.listCachedAt = out, time.Now()
	return out, nil
}

// forgetShipmentList descarta la lista guardada para que la próxima consulta la traiga de nuevo.
func (c *Client) forgetShipmentList() {
	c.listMu.Lock()
	c.listCachedAt = time.Time{}
	c.listMu.Unlock()
}

// fetchShipments descarga todas las páginas, con una pausa entre ellas para respetar el límite de la API.
func (c *Client) fetchShipments(ctx context.Context) (ShipmentList, error) {
	var out ShipmentList
	seenIncluded := map[string]bool{}
	for page := 1; ; page++ {
		if page > 1 {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(shipmentPagePause):
			}
		}
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
