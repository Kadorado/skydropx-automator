package parser

import (
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"skydropx-automator/internal/domain"
)

var (
	reNombre       = regexp.MustCompile(`(?im)^\s*(?:receptor|nombre(?:\s+completo)?|destinatario|cliente)\s*:\s*(.+)$`)
	reDocumento    = regexp.MustCompile(`(?im)^\s*(?:documento|c[eé]dula|cc|nit|identificaci[oó]n)\s*:\s*([0-9.\-\s]+)$`)
	reTelefono     = regexp.MustCompile(`(?im)^\s*(?:tel[eé]fono|cel|celular|whatsapp|m[oó]vil)\s*:\s*([0-9+\s\-()]+)$`)
	reDepartamento = regexp.MustCompile(`(?im)^\s*(?:departamento|depto|estado)\s*:\s*(.+)$`)
	reCiudad       = regexp.MustCompile(`(?im)^\s*(?:ciudad|municipio)\s*:\s*(.+)$`)
	reBarrio       = regexp.MustCompile(`(?im)^\s*(?:barrio|sector|urbanizaci[oó]n)\s*:\s*(.+)$`)
	reDireccion    = regexp.MustCompile(`(?im)^\s*(?:direcci[oó]n|dir)\s*:\s*(.+)$`)
	reCorreo       = regexp.MustCompile(`(?im)^\s*(?:correo(?:\s+electr[oó]nico)?|email|e-mail)\s*:\s*([^\s]+)$`)
	reCantidad     = regexp.MustCompile(`(?im)^\s*(?:cantidad|cant|unidades|paquetes)\s*:\s*(\d+)`)
	reCodigoPostal = regexp.MustCompile(`(?im)^\s*(?:c[oó]digo\s+postal|cod(?:igo)?\.?\s*postal|cp)\s*:\s*(\d{5,6})\b`)
	reValor        = regexp.MustCompile(`(?im)^\s*(?:valor(?:\s+unitario)?|precio(?:\s+unitario)?)\s*:\s*\$?\s*([\d][\d.,]*)`)
)

// ParseWhatsAppMessage extrae los datos sin inventar campos faltantes.
func ParseWhatsAppMessage(raw string) domain.ParseResponse {
	raw = normalizeWhatsApp(raw)
	titleCaser := cases.Title(language.Spanish)
	var warnings []string

	data := domain.ShipmentData{
		Pais:     "Colombia",
		Cantidad: 1,
	}

	if m := reNombre.FindStringSubmatch(raw); len(m) > 1 {
		data.Nombre = cleanSpaces(m[1])
	}

	if m := reDocumento.FindStringSubmatch(raw); len(m) > 1 {
		data.Documento = cleanDigits(m[1])
	}

	if m := reTelefono.FindStringSubmatch(raw); len(m) > 1 {
		tel := cleanDigits(m[1])
		if len(tel) == 12 && strings.HasPrefix(tel, "57") {
			tel = tel[2:]
		}
		data.Telefono = tel
	}

	if m := reBarrio.FindStringSubmatch(raw); len(m) > 1 {
		data.Barrio = titleCaser.String(strings.ToLower(cleanSpaces(m[1])))
	}

	if m := reDireccion.FindStringSubmatch(raw); len(m) > 1 {
		data.Direccion = cleanSpaces(m[1])
	}

	if m := reCorreo.FindStringSubmatch(raw); len(m) > 1 {
		email := strings.ToLower(cleanSpaces(m[1]))
		if _, err := mail.ParseAddress(email); err == nil {
			data.Correo = email
		} else {
			data.Correo = email
			warnings = append(warnings, "El correo electrónico extraído no tiene un formato válido; revísalo.")
		}
	}

	if m := reCantidad.FindStringSubmatch(raw); len(m) > 1 {
		if qty, err := strconv.Atoi(m[1]); err == nil && qty > 0 {
			data.Cantidad = qty
		}
	}

	// Valor unitario en pesos. Acepta "60000", "60.000" o "$60,000" (COP no usa decimales).
	if m := reValor.FindStringSubmatch(raw); len(m) > 1 {
		if v, err := strconv.ParseFloat(cleanDigits(m[1]), 64); err == nil && v > 0 {
			data.ValorUnitario = v
		}
	}

	// Departamento explícito (si viene en una línea aparte)
	explicitDept := ""
	if m := reDepartamento.FindStringSubmatch(raw); len(m) > 1 {
		val := cleanSpaces(m[1])
		// Evitar confundir "ESTADO: Pendiente de despacho" con departamento
		if !strings.Contains(strings.ToLower(val), "pendiente") && !strings.Contains(strings.ToLower(val), "despacho") {
			explicitDept = titleCaser.String(strings.ToLower(val))
		}
	}

	// Ciudad y resolución de departamento
	if m := reCiudad.FindStringSubmatch(raw); len(m) > 1 {
		city, dept, postal := ResolveCityAndDept(cleanSpaces(m[1]), titleCaser)
		data.Ciudad = city
		data.Departamento = dept
		data.CodigoPostal = postal
	}

	if explicitDept != "" {
		data.Departamento = explicitDept
	}

	// Un código postal explícito reemplaza el de referencia de la tabla de ciudades.
	if m := reCodigoPostal.FindStringSubmatch(raw); len(m) > 1 {
		data.CodigoPostal = m[1]
	}

	missing := ValidateShipmentData(data)
	return domain.ParseResponse{
		Data:          data,
		MissingFields: missing,
		Warnings:      warnings,
	}
}

// ValidateShipmentData devuelve la lista de campos obligatorios faltantes o inválidos.
func ValidateShipmentData(d domain.ShipmentData) []string {
	var missing []string
	if strings.TrimSpace(d.Nombre) == "" {
		missing = append(missing, "nombre")
	}
	if strings.TrimSpace(d.Documento) == "" {
		missing = append(missing, "documento")
	}
	if len(cleanDigits(d.Telefono)) < 10 {
		missing = append(missing, "telefono")
	}
	if strings.TrimSpace(d.Departamento) == "" {
		missing = append(missing, "departamento")
	}
	if strings.TrimSpace(d.Ciudad) == "" {
		missing = append(missing, "ciudad")
	}
	if strings.TrimSpace(d.Direccion) == "" {
		missing = append(missing, "direccion")
	}
	if d.ValorUnitario <= 0 {
		missing = append(missing, "valor_unitario")
	}
	if d.Correo != "" {
		if _, err := mail.ParseAddress(d.Correo); err != nil {
			missing = append(missing, "correo (formato inválido)")
		}
	}
	return missing
}

func cleanSpaces(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func cleanDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
