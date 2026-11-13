package domain

import "time"

// ShipmentData representa los datos del destinatario extraídos y editables.
type ShipmentData struct {
	Nombre       string `json:"nombre"`
	Documento    string `json:"documento"`
	Telefono     string `json:"telefono"`
	Correo       string `json:"correo"`
	Pais         string `json:"pais"`
	Departamento string `json:"departamento"`
	Ciudad       string `json:"ciudad"`
	CodigoPostal string `json:"codigo_postal"`
	Barrio       string `json:"barrio"`
	Direccion    string `json:"direccion"`
	Cantidad     int    `json:"cantidad"`
	// ValorUnitario en COP por unidad; el valor declarado del paquete = ValorUnitario × Cantidad.
	ValorUnitario float64 `json:"valor_unitario"`
	Referencia    string  `json:"referencia"`
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
