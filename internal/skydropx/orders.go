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

// maxQuantityLookups limita cuántas órdenes se consultan por solicitud (una página de "Mis envíos").
const maxQuantityLookups = 20

var (
	quantityMu    sync.Mutex
	quantityCache = map[string]int{}     // la cantidad de una orden no cambia: se guarda mientras corra la app
	productCache  = map[string]float64{} // valor del producto de la orden (cantidad × precio de cada artículo)
)

// OrderQuantities devuelve la cantidad total de artículos de cada orden (suma de "quantity" de sus items).
// Las órdenes que no se pueden consultar se omiten del resultado.
func (c *Client) OrderQuantities(ctx context.Context, orderIDs []string) (map[string]int, error) {
	return c.orderQuantitiesUpTo(ctx, orderIDs, maxQuantityLookups)
}

// orderQuantitiesUpTo es OrderQuantities con un tope propio de consultas nuevas (las de caché no cuentan).
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
			time.Sleep(trackSpacing) // límite de 2 solicitudes por segundo
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

// orderProducts devuelve el valor del producto de las órdenes ya consultadas (sin pedir nada nuevo).
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

// orderQuantity devuelve la cantidad de artículos de la orden y el valor del producto (cantidad × precio).
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
