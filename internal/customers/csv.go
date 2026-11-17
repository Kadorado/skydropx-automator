package customers

import (
	"encoding/csv"
	"fmt"
	"io"
	"time"
)

// colombia is Colombia's time zone (UTC-5, no daylight saving).
var colombia = time.FixedZone("COT", -5*3600)

// WriteCSV writes the list as a spreadsheet (headers in Spanish, for the people who read it). It starts
// with a UTF-8 BOM so Excel shows accents correctly.
func WriteCSV(w io.Writer, list []Customer) error {
	if _, err := io.WriteString(w, "\xef\xbb\xbf"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"nombre", "cedula", "telefono", "correo", "departamento", "ciudad", "barrio", "direccion",
		"autoriza_datos", "fecha_autorizacion", "como_autorizo", "baja", "pedidos", "primer_pedido", "ultimo_pedido", "fuente"})
	day := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.In(colombia).Format("2006-01-02")
	}
	for _, c := range list {
		consents, optedOutCount := "no", ""
		if c.Consent.Given {
			consents = "si"
		}
		if c.OptOutAt != nil {
			optedOutCount = day(*c.OptOutAt)
		}
		_ = cw.Write([]string{c.Name, c.DocumentID, c.Phone, c.Email, c.Department, c.City, c.Neighborhood, c.Address,
			consents, day(c.Consent.Date), c.Consent.Evidence, optedOutCount, fmt.Sprint(len(c.Orders)),
			day(c.FirstOrderAt), day(c.LastOrderAt), c.Source})
	}
	cw.Flush()
	return cw.Error()
}
