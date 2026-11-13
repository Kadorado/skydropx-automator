package skydropx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// OrderShipmentIDs devuelve los IDs de envío que Skydropx asoció a una orden (GET /api/v1/orders/{id}).
// Mientras la orden no tenga envío, la lista va vacía.
func (c *Client) OrderShipmentIDs(ctx context.Context, orderID string) ([]string, error) {
	if c.isMock() {
		return nil, ErrMockMode
	}
	if strings.TrimSpace(orderID) == "" {
		return nil, fmt.Errorf("ID de orden vacío")
	}
	body, status, err := c.get(ctx, c.cfg.SkydropxBaseURL+"/api/v1/orders/"+url.PathEscape(orderID))
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Skydropx respondió HTTP %d al consultar la orden", status)
	}
	var raw struct {
		Data struct {
			Relationships struct {
				Shipments struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"shipments"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("respuesta inesperada de Skydropx al consultar la orden: %w", err)
	}
	ids := make([]string, 0, len(raw.Data.Relationships.Shipments.Data))
	for _, s := range raw.Data.Relationships.Shipments.Data {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	return ids, nil
}
