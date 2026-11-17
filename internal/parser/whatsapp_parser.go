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
	reName         = regexp.MustCompile(`(?im)^\s*(?:receptor|nombre(?:\s+completo)?|destinatario|cliente)\s*:\s*(.+)$`)
	reDocument     = regexp.MustCompile(`(?im)^\s*(?:documento|c[eé]dula|cc|nit|identificaci[oó]n)\s*:\s*([0-9.\-\s]+)$`)
	rePhone        = regexp.MustCompile(`(?im)^\s*(?:tel[eé]fono|cel|celular|whatsapp|m[oó]vil)\s*:\s*([0-9+\s\-()]+)$`)
	reDepartment   = regexp.MustCompile(`(?im)^\s*(?:departamento|depto|estado)\s*:\s*(.+)$`)
	reCity         = regexp.MustCompile(`(?im)^\s*(?:ciudad|municipio)\s*:\s*(.+)$`)
	reNeighborhood = regexp.MustCompile(`(?im)^\s*(?:barrio|sector|urbanizaci[oó]n)\s*:\s*(.+)$`)
	reAddress      = regexp.MustCompile(`(?im)^\s*(?:direcci[oó]n|dir)\s*:\s*(.+)$`)
	reEmail        = regexp.MustCompile(`(?im)^\s*(?:correo(?:\s+electr[oó]nico)?|email|e-mail)\s*:\s*([^\s]+)$`)
	reQuantity     = regexp.MustCompile(`(?im)^\s*(?:cantidad|cant|unidades|paquetes)\s*:\s*(\d+)`)
	rePostalCode   = regexp.MustCompile(`(?im)^\s*(?:c[oó]digo\s+postal|cod(?:igo)?\.?\s*postal|cp)\s*:\s*(\d{5,6})\b`)
	reConsent      = regexp.MustCompile(`(?im)^\s*(?:autoriza(?:\s+(?:datos|tratamiento(?:\s+de\s+datos)?))?|acepta\s+datos|autorizaci[oó]n(?:\s+de\s+datos)?)\s*:\s*(.+)$`)
	reSource       = regexp.MustCompile(`(?im)^\s*(?:fuente|origen|lleg[oó]\s+por|canal)\s*:\s*(.+)$`)
	reUnitPrice    = regexp.MustCompile(`(?im)^\s*(?:valor(?:\s+unitario)?|precio(?:\s+unitario)?)\s*:\s*\$?\s*([\d][\d.,]*)`)
)

// ParseWhatsAppMessage extracts the data without inventing missing fields.
func ParseWhatsAppMessage(raw string) domain.ParseResponse {
	raw = normalizeWhatsApp(raw)
	titleCaser := cases.Title(language.Spanish)
	var warnings []string

	data := domain.ShipmentData{
		Country:  "Colombia",
		Quantity: 1,
	}

	if m := reName.FindStringSubmatch(raw); len(m) > 1 {
		data.Name = cleanSpaces(m[1])
	}

	if m := reDocument.FindStringSubmatch(raw); len(m) > 1 {
		data.Document = cleanDigits(m[1])
	}

	if m := rePhone.FindStringSubmatch(raw); len(m) > 1 {
		tel := cleanDigits(m[1])
		if len(tel) == 12 && strings.HasPrefix(tel, "57") {
			tel = tel[2:]
		}
		data.Phone = tel
	}

	if m := reNeighborhood.FindStringSubmatch(raw); len(m) > 1 {
		data.Neighborhood = titleCaser.String(strings.ToLower(cleanSpaces(m[1])))
	}

	if m := reAddress.FindStringSubmatch(raw); len(m) > 1 {
		data.Address = cleanSpaces(m[1])
	}

	if m := reConsent.FindStringSubmatch(raw); len(m) > 1 {
		switch strings.Trim(strings.ToLower(cleanSpaces(m[1])), ".!*_ ") {
		case "si", "sí", "s", "x", "yes", "acepta", "autoriza", "ok":
			data.DataConsent = "si"
		case "no", "n":
			data.DataConsent = "no"
		}
	}
	if m := reSource.FindStringSubmatch(raw); len(m) > 1 {
		data.Source = strings.ToLower(strings.Trim(cleanSpaces(m[1]), ".*_ "))
	}
	if m := reEmail.FindStringSubmatch(raw); len(m) > 1 {
		email := strings.ToLower(cleanSpaces(m[1]))
		if _, err := mail.ParseAddress(email); err == nil {
			data.Email = email
		} else {
			data.Email = email
			warnings = append(warnings, "El correo electrónico extraído no tiene un formato válido; revísalo.")
		}
	}

	if m := reQuantity.FindStringSubmatch(raw); len(m) > 1 {
		if qty, err := strconv.Atoi(m[1]); err == nil && qty > 0 {
			data.Quantity = qty
		}
	}

	// Unit price in pesos. Accepts "60000", "60.000" or "$60,000" (COP has no decimals).
	if m := reUnitPrice.FindStringSubmatch(raw); len(m) > 1 {
		if v, err := strconv.ParseFloat(cleanDigits(m[1]), 64); err == nil && v > 0 {
			data.UnitPrice = v
		}
	}

	// Explicit department (if it comes on a separate line)
	explicitDept := ""
	if m := reDepartment.FindStringSubmatch(raw); len(m) > 1 {
		val := cleanSpaces(m[1])
		// Avoid confusing "ESTADO: Pendiente de despacho" with a department
		if !strings.Contains(strings.ToLower(val), "pendiente") && !strings.Contains(strings.ToLower(val), "despacho") {
			explicitDept = titleCaser.String(strings.ToLower(val))
		}
	}

	// City and department resolution
	if m := reCity.FindStringSubmatch(raw); len(m) > 1 {
		city, dept, postal := ResolveCityAndDept(cleanSpaces(m[1]), titleCaser)
		data.City = city
		data.Department = dept
		data.PostalCode = postal
	}

	if explicitDept != "" {
		data.Department = explicitDept
		// With the department known, a name repeated in several departments now resolves.
		if postal := LookupPostalCode(data.City, data.Department); postal != "" {
			data.PostalCode = postal
		}
	}

	// An explicit postal code replaces the reference one from the city table.
	if m := rePostalCode.FindStringSubmatch(raw); len(m) > 1 {
		data.PostalCode = m[1]
	}

	missing := ValidateShipmentData(data)
	return domain.ParseResponse{
		Data:          data,
		MissingFields: missing,
		Warnings:      warnings,
	}
}

// ValidateShipmentData returns the list of missing or invalid required fields.
func ValidateShipmentData(d domain.ShipmentData) []string {
	var missing []string
	if strings.TrimSpace(d.Name) == "" {
		missing = append(missing, "nombre")
	}
	if strings.TrimSpace(d.Document) == "" {
		missing = append(missing, "documento")
	}
	if len(cleanDigits(d.Phone)) < 10 {
		missing = append(missing, "telefono")
	}
	if strings.TrimSpace(d.Department) == "" {
		missing = append(missing, "departamento")
	}
	if strings.TrimSpace(d.City) == "" {
		missing = append(missing, "ciudad")
	}
	if strings.TrimSpace(d.Address) == "" {
		missing = append(missing, "direccion")
	}
	if d.UnitPrice <= 0 {
		missing = append(missing, "valor_unitario")
	}
	if d.Email != "" {
		if _, err := mail.ParseAddress(d.Email); err != nil {
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
