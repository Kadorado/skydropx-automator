package skydropx

import (
	"context"
	"strings"
	"time"
)

// CODShipment es el estado del cobro contra entrega de un envío con guía.
type CODShipment struct {
	ID       string
	Tracking string
	Carrier  string
	Customer string
	Phone    string
	City     string
	OrderID  string
	Status   string  // pending | collected | failed | cancelled | vacío
	Reported float64 // monto de contra entrega que reporta Skydropx (a veces con el envío, a veces sin él)
	Cost     float64 // costo de la guía
}

// CODShipments devuelve el estado del cobro de todos los envíos con guía de la cuenta.
func (c *Client) CODShipments(ctx context.Context) ([]CODShipment, error) {
	if c.isMock() {
		return nil, ErrMockMode
	}
	list, err := c.ListShipments(ctx)
	if err != nil {
		return nil, err
	}
	all := Period{To: time.Now().In(colombia).Add(24 * time.Hour)}
	var out []CODShipment
	for _, s := range withLabel(financeShipments(list, all)) {
		status := s.OnDelivery
		if s.Cancelled {
			status = "cancelled"
		}
		out = append(out, CODShipment{
			ID: s.ID, Tracking: s.Tracking, Carrier: s.Carrier, Customer: s.Customer, Phone: s.Phone, City: s.City, OrderID: s.OrderID,
			Status: status, Reported: s.CODAmount, Cost: s.Cost,
		})
	}
	return out, nil
}

// CODAmounts devuelve lo que la transportadora le cobra al cliente y el valor del producto
// (0 si no se pudo consultar la orden), con la misma cuenta que Finanzas.
func (c *Client) CODAmounts(ctx context.Context, s CODShipment) (pays, product float64) {
	if s.OrderID != "" {
		_, _ = c.orderQuantitiesUpTo(ctx, []string{s.OrderID}, 1)
		product = orderProducts([]string{s.OrderID})[s.OrderID]
	}
	fs := financeShipment{CODAmount: s.Reported, Cost: s.Cost, Product: product}
	return fs.customerPays(), product
}

// Problema es un envío abierto que hay que gestionar con el cliente.
type Problema struct {
	CODShipment
	Kind      string // devolucion | intento | oficina | novedad
	LastEvent string // último evento de rastreo
	LastDate  string
	History   []TrackEvent // últimos eventos, del más nuevo al más antiguo
}

// Problemas revisa los envíos con guía creados desde since que no se han entregado ni cobrado y devuelve
// los que están en devolución o sin cobrar, con intento de entrega fallido, en oficina o con novedad.
// Consulta el rastreo de cada uno (con caché y respetando el límite de la API).
func (c *Client) Problemas(ctx context.Context, since time.Time) ([]Problema, int, error) {
	if c.isMock() {
		return nil, 0, ErrMockMode
	}
	list, err := c.ListShipments(ctx)
	if err != nil {
		return nil, 0, err
	}
	p := Period{From: since, To: time.Now().In(colombia).Add(24 * time.Hour)}
	statusMu.Lock()
	defer statusMu.Unlock()
	var out []Problema
	revisados := 0
	for _, s := range withLabel(financeShipments(list, p)) {
		if s.Cancelled || s.OnDelivery == "collected" {
			continue
		}
		pr := Problema{CODShipment: CODShipment{
			ID: s.ID, Tracking: s.Tracking, Carrier: s.Carrier, Customer: s.Customer, Phone: s.Phone, City: s.City, OrderID: s.OrderID,
			Status: s.OnDelivery, Reported: s.CODAmount, Cost: s.Cost,
		}}
		revisados++
		st, err := c.trackingStatus(ctx, s.Tracking, s.Carrier)
		if err != nil && ctx.Err() != nil {
			return out, revisados, ctx.Err()
		}
		pr.LastEvent, pr.LastDate, pr.History = st.LastEvent, st.LastDate, st.History
		pr.Kind = problemaKind(s.OnDelivery, st.Raw, st.LastEvent)
		if pr.Kind != "" {
			out = append(out, pr)
		}
	}
	return out, revisados, nil
}

// EstadoPorCobrar dice dónde va un envío por cobrar según su rastreo: "oficina" (para reclamar),
// "novedad" (intento fallido, novedad o devolución) o "camino". Si no se puede rastrear, "camino".
func (c *Client) EstadoPorCobrar(ctx context.Context, tracking, carrier string) string {
	if c.isMock() || tracking == "" {
		return "camino"
	}
	statusMu.Lock()
	defer statusMu.Unlock()
	st, err := c.trackingStatus(ctx, tracking, carrier)
	if err != nil {
		return "camino"
	}
	switch problemaKind("pending", st.Raw, st.LastEvent) {
	case "oficina":
		return "oficina"
	case "":
		return "camino"
	default:
		return "novedad"
	}
}

// problemaKind clasifica un envío según su cobro y su último evento de rastreo ("" si va bien).
func problemaKind(onDelivery, raw, lastEvent string) string {
	switch {
	case raw == "delivered":
		return ""
	case onDelivery == "failed" || raw == "in_return" || strings.Contains(strings.ToLower(lastEvent), "devoluci"):
		return "devolucion"
	case raw == "delivery_attempt":
		return "intento"
	case raw == "delivered_to_branch":
		return "oficina"
	case raw == "exception":
		return "novedad"
	}
	return ""
}
