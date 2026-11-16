package skydropx

import (
	"context"
	"sort"
	"strings"
	"unicode"
)

// Antecedente es un envío anterior de un cliente que salió mal: no pagó (devolución o sin cobrar)
// o se demoró en recoger el paquete en la oficina de la transportadora.
type Antecedente struct {
	CODShipment
	Tipo          string // no_pago | oficina
	DiasEnOficina int    // solo en "oficina"
}

// maxRastreosHistorial limita cuántos envíos entregados de un cliente se rastrean para medir la oficina.
const maxRastreosHistorial = 8

func soloDigitos(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ClaveDocumento normaliza una cédula para compararla (solo dígitos; "" si es muy corta para ser una).
func ClaveDocumento(s string) string {
	if d := soloDigitos(s); len(d) >= 5 {
		return d
	}
	return ""
}

// ClaveTelefono normaliza un teléfono para compararlo: los últimos 10 dígitos ("" si tiene menos de 7).
func ClaveTelefono(s string) string {
	d := soloDigitos(s)
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	if len(d) < 7 {
		return ""
	}
	return d
}

// HistorialCliente busca los envíos anteriores con esa cédula o ese teléfono y devuelve los que
// salieron mal: los que no se cobraron y los que esperaron en oficina diasOficina días o más antes de
// que el cliente los recogiera. También devuelve cuántos envíos anteriores encontró en total.
// Los envíos por cobrar no cuentan: todavía no se sabe cómo terminan.
func (c *Client) HistorialCliente(ctx context.Context, documento, telefono string, diasOficina int) (malos []Antecedente, total int, err error) {
	doc, tel := ClaveDocumento(documento), ClaveTelefono(telefono)
	if doc == "" && tel == "" {
		return nil, 0, nil
	}
	envios, err := c.CODShipments(ctx)
	if err != nil {
		return nil, 0, err
	}
	var suyos []CODShipment
	for _, e := range envios {
		if e.Status == "cancelled" || e.Tracking == "" {
			continue
		}
		if (doc != "" && ClaveDocumento(e.TaxID) == doc) || (tel != "" && ClaveTelefono(e.Phone) == tel) {
			suyos = append(suyos, e)
		}
	}
	sort.SliceStable(suyos, func(i, j int) bool { return suyos[i].Date > suyos[j].Date }) // más nuevos primero

	rastreados := 0
	for _, e := range suyos {
		switch e.Status {
		case "failed":
			malos = append(malos, Antecedente{CODShipment: e, Tipo: "no_pago"})
		case "collected":
			if rastreados >= maxRastreosHistorial || ctx.Err() != nil {
				continue
			}
			rastreados++
			st, err := c.Rastreo(ctx, e.Tracking, e.Carrier)
			if err == nil && st.DiasEnOficina >= diasOficina {
				malos = append(malos, Antecedente{CODShipment: e, Tipo: "oficina", DiasEnOficina: st.DiasEnOficina})
			}
		}
	}
	return malos, len(suyos), nil
}
