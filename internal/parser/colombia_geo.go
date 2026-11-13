package parser

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

type GeoInfo struct {
	City       string
	Department string
	PostalCode string
}

// Mapa de municipios principales y frecuentes en Colombia (clave normalizada sin tildes en minúsculas)
var colombiaCities = map[string]GeoInfo{
	"quibdo":        {City: "Quibdó", Department: "Chocó", PostalCode: "270001"},
	"istmina":       {City: "Istmina", Department: "Chocó", PostalCode: "274010"},
	"tad":           {City: "Tadó", Department: "Chocó", PostalCode: "273010"},
	"tado":          {City: "Tadó", Department: "Chocó", PostalCode: "273010"},
	"condoto":       {City: "Condoto", Department: "Chocó", PostalCode: "274030"},
	"medellin":      {City: "Medellín", Department: "Antioquia", PostalCode: "050001"},
	"bello":         {City: "Bello", Department: "Antioquia", PostalCode: "051050"},
	"envigado":      {City: "Envigado", Department: "Antioquia", PostalCode: "055420"},
	"itagui":        {City: "Itagüí", Department: "Antioquia", PostalCode: "055410"},
	"rionegro":      {City: "Rionegro", Department: "Antioquia", PostalCode: "054040"},
	"apartado":      {City: "Apartadó", Department: "Antioquia", PostalCode: "057840"},
	"turbo":         {City: "Turbo", Department: "Antioquia", PostalCode: "057860"},
	"bogota":        {City: "Bogotá D.C.", Department: "Bogotá D.C.", PostalCode: "110111"},
	"bogota dc":     {City: "Bogotá D.C.", Department: "Bogotá D.C.", PostalCode: "110111"},
	"cali":          {City: "Cali", Department: "Valle del Cauca", PostalCode: "760001"},
	"buenaventura":  {City: "Buenaventura", Department: "Valle del Cauca", PostalCode: "764501"},
	"palmira":       {City: "Palmira", Department: "Valle del Cauca", PostalCode: "763531"},
	"barranquilla":  {City: "Barranquilla", Department: "Atlántico", PostalCode: "080001"},
	"soledad":       {City: "Soledad", Department: "Atlántico", PostalCode: "083001"},
	"cartagena":     {City: "Cartagena", Department: "Bolívar", PostalCode: "130001"},
	"bucaramanga":   {City: "Bucaramanga", Department: "Santander", PostalCode: "680001"},
	"floridablanca": {City: "Floridablanca", Department: "Santander", PostalCode: "681001"},
	"cucuta":        {City: "Cúcuta", Department: "Norte de Santander", PostalCode: "540001"},
	"pereira":       {City: "Pereira", Department: "Risaralda", PostalCode: "660001"},
	"dosquebradas":  {City: "Dosquebradas", Department: "Risaralda", PostalCode: "661001"},
	"manizales":     {City: "Manizales", Department: "Caldas", PostalCode: "170001"},
	"armenia":       {City: "Armenia", Department: "Quindío", PostalCode: "630001"},
	"ibague":        {City: "Ibagué", Department: "Tolima", PostalCode: "730001"},
	"neiva":         {City: "Neiva", Department: "Huila", PostalCode: "410001"},
	"villavicencio": {City: "Villavicencio", Department: "Meta", PostalCode: "500001"},
	"pasto":         {City: "Pasto", Department: "Nariño", PostalCode: "520001"},
	"monteria":      {City: "Montería", Department: "Córdoba", PostalCode: "230001"},
	"sincelejo":     {City: "Sincelejo", Department: "Sucre", PostalCode: "700001"},
	"valledupar":    {City: "Valledupar", Department: "Cesar", PostalCode: "200001"},
	"santa marta":   {City: "Santa Marta", Department: "Magdalena", PostalCode: "470001"},
	"riohacha":      {City: "Riohacha", Department: "La Guajira", PostalCode: "440001"},
	"tunja":         {City: "Tunja", Department: "Boyacá", PostalCode: "150001"},
	"popayan":       {City: "Popayán", Department: "Cauca", PostalCode: "190001"},
	"florencia":     {City: "Florencia", Department: "Caquetá", PostalCode: "180001"},
	"yopal":         {City: "Yopal", Department: "Casanare", PostalCode: "850001"},
}

var colombiaDepartments = map[string]string{
	"amazonas":           "Amazonas",
	"antioquia":          "Antioquia",
	"arauca":             "Arauca",
	"atlantico":          "Atlántico",
	"bogota":             "Bogotá D.C.",
	"bogota dc":          "Bogotá D.C.",
	"bolivar":            "Bolívar",
	"boyaca":             "Boyacá",
	"caldas":             "Caldas",
	"caqueta":            "Caquetá",
	"casanare":           "Casanare",
	"cauca":              "Cauca",
	"cesar":              "Cesar",
	"choco":              "Chocó",
	"cordoba":            "Córdoba",
	"cundinamarca":       "Cundinamarca",
	"guainia":            "Guainía",
	"guaviare":           "Guaviare",
	"huila":              "Huila",
	"la guajira":         "La Guajira",
	"guajira":            "La Guajira",
	"magdalena":          "Magdalena",
	"meta":               "Meta",
	"narino":             "Nariño",
	"norte de santander": "Norte de Santander",
	"putumayo":           "Putumayo",
	"quindio":            "Quindío",
	"risaralda":          "Risaralda",
	"san andres":         "San Andrés y Providencia",
	"santander":          "Santander",
	"sucre":              "Sucre",
	"tolima":             "Tolima",
	"valle":              "Valle del Cauca",
	"valle del cauca":    "Valle del Cauca",
	"vaupes":             "Vaupés",
	"vichada":            "Vichada",
}

func stripAccents(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	res, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return res
}

func normalizeKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = stripAccents(s)
	s = strings.ReplaceAll(s, ",", " ")
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "/", " ")
	return strings.Join(strings.Fields(s), " ")
}

// ResolveCityAndDept interpreta textos como "QUIBDO CHOCO", "Medellín, Antioquia" o "Quibdó".
func ResolveCityAndDept(raw string, titleCaser cases.Caser) (city, dept, postal string) {
	normInput := normalizeKey(raw)
	if normInput == "" {
		return "", "", ""
	}

	// 1. Coincidencia exacta con ciudad conocida
	if info, ok := colombiaCities[normInput]; ok {
		return info.City, info.Department, info.PostalCode
	}

	// 2. Separar si viene "CIUDAD DEPARTAMENTO" (ej. "quibdo choco" o "santa marta magdalena")
	// Probamos sufijos de departamento conocidos desde los más largos a los más cortos
	for deptKey, deptProper := range colombiaDepartments {
		suffix := " " + deptKey
		if strings.HasSuffix(normInput, suffix) {
			cityPart := strings.TrimSpace(strings.TrimSuffix(normInput, suffix))
			if info, ok := colombiaCities[cityPart]; ok {
				return info.City, deptProper, info.PostalCode
			}
			return titleCaser.String(cityPart), deptProper, ""
		}
	}

	// 3. Fallback: capitalizar lo que venga sin inventar departamento si no se reconoce
	return titleCaser.String(strings.ToLower(strings.TrimSpace(raw))), "", ""
}

// LookupPostalCode intenta obtener un código postal de referencia si el usuario editó la ciudad.
func LookupPostalCode(city string) string {
	if info, ok := colombiaCities[normalizeKey(city)]; ok {
		return info.PostalCode
	}
	return ""
}
