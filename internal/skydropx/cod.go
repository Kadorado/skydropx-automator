package skydropx

import (
	"context"
	"time"
)

// CODShipment es el estado del cobro contra entrega de un envío con guía.
type CODShipment struct {
	ID       string
	Tracking string
	Customer string
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
			ID: s.ID, Tracking: s.Tracking, Customer: s.Customer, OrderID: s.OrderID,
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
