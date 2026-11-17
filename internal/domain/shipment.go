package domain

import "time"

// ShipmentData holds the recipient's extracted, editable data.
type ShipmentData struct {
	Name         string `json:"nombre"`
	Document     string `json:"documento"`
	Phone        string `json:"telefono"`
	Email        string `json:"correo"`
	Country      string `json:"pais"`
	Department   string `json:"departamento"`
	City         string `json:"ciudad"`
	PostalCode   string `json:"codigo_postal"`
	Neighborhood string `json:"barrio"`
	Address      string `json:"direccion"`
	Quantity     int    `json:"cantidad"`
	// UnitPrice in COP per unit; the package's declared value = UnitPrice × Quantity.
	UnitPrice float64 `json:"valor_unitario"`
	Reference string  `json:"referencia"`
	// DataConsent is the customer's answer about using their data for marketing: "si", "no" or empty.
	DataConsent string `json:"autoriza_datos,omitempty"`
	// Source is where the customer came from (facebook, whatsapp, referido…), lowercase; empty if not given.
	Source string `json:"fuente,omitempty"`
}

type ParseRequest struct {
	RawMessage string `json:"raw_message"`
}

type ParseResponse struct {
	Data          ShipmentData `json:"data"`
	MissingFields []string     `json:"missing_fields"`
	Warnings      []string     `json:"warnings,omitempty"`
}

type CreateShipmentRequest struct {
	IdempotencyKey string       `json:"idempotency_key"`
	Data           ShipmentData `json:"data"`
}

type ShipmentResult struct {
	ID             string    `json:"id"`
	Status         string    `json:"status"` // "created", "pending_label", "error"
	RecipientName  string    `json:"recipient_name"`
	Destination    string    `json:"destination"`
	Carrier        string    `json:"carrier"`
	TrackingNumber string    `json:"tracking_number"`
	LabelURL       string    `json:"label_url"`
	EstimatedCost  string    `json:"estimated_cost,omitempty"`
	IsMock         bool      `json:"is_mock"`
	CreatedAt      time.Time `json:"created_at"`
	ErrorMessage   string    `json:"error_message,omitempty"`
}
