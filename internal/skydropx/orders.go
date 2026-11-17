package skydropx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OrderShipmentIDs returns the shipment IDs Skydropx linked to an order (GET /api/v1/orders/{id}).
// While the order has no shipment, the list is empty.
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

// maxQuantityLookups limits how many orders are looked up per request (one page of "Mis envíos").
const maxQuantityLookups = 20

var (
	quantityMu    sync.Mutex
	quantityCache = map[string]int{}     // an order's quantity doesn't change: it is kept while the app runs
	productCache  = map[string]float64{} // order's product value (quantity × price of each item)
)

// OrderQuantities returns the total item count of each order (sum of the "quantity" of its items).
// Orders that can't be looked up are left out of the result.
func (c *Client) OrderQuantities(ctx context.Context, orderIDs []string) (map[string]int, error) {
	return c.orderQuantitiesUpTo(ctx, orderIDs, maxQuantityLookups)
}

// orderQuantitiesUpTo is OrderQuantities with its own cap on new lookups (cached ones don't count).
func (c *Client) orderQuantitiesUpTo(ctx context.Context, orderIDs []string, maxLookups int) (map[string]int, error) {
	if c.isMock() {
		return nil, ErrMockMode
	}
	quantityMu.Lock()
	defer quantityMu.Unlock()

	out := map[string]int{}
	fetched := 0
	for _, id := range orderIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if q, ok := quantityCache[id]; ok {
			out[id] = q
			continue
		}
		if fetched >= maxLookups || ctx.Err() != nil {
			break
		}
		if fetched > 0 {
			time.Sleep(trackSpacing) // limit of 2 requests per second
		}
		fetched++
		q, product, err := c.orderQuantity(ctx, id)
		if err != nil {
			continue
		}
		quantityCache[id] = q
		if product > 0 {
			productCache[id] = product
		}
		out[id] = q
	}
	return out, nil
}

// orderProducts returns the product value of orders already looked up (without requesting anything new).
func orderProducts(orderIDs []string) map[string]float64 {
	quantityMu.Lock()
	defer quantityMu.Unlock()
	out := map[string]float64{}
	for _, id := range orderIDs {
		if v, ok := productCache[id]; ok {
			out[id] = v
		}
	}
	return out
}

// orderQuantity returns the order's item count and product value (quantity × price).
func (c *Client) orderQuantity(ctx context.Context, orderID string) (int, float64, error) {
	body, status, err := c.get(ctx, c.cfg.SkydropxBaseURL+"/api/v1/orders/"+url.PathEscape(orderID))
	if err != nil {
		return 0, 0, err
	}
	if status < 200 || status >= 300 {
		return 0, 0, fmt.Errorf("Skydropx respondió HTTP %d al consultar la orden", status)
	}
	var raw struct {
		Included []struct {
			Type       string `json:"type"`
			Attributes struct {
				Quantity int             `json:"quantity"`
				Price    json.RawMessage `json:"price"`
			} `json:"attributes"`
		} `json:"included"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, 0, fmt.Errorf("respuesta inesperada de Skydropx al consultar la orden: %w", err)
	}
	total := 0
	product := 0.0
	for _, inc := range raw.Included {
		if inc.Type == "item" || inc.Type == "items" {
			total += inc.Attributes.Quantity
			product += float64(inc.Attributes.Quantity) * parseAmount(inc.Attributes.Price)
		}
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("la orden no tiene artículos")
	}
	return total, product, nil
}
