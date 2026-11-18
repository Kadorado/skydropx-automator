package parser

import (
	_ "embed"
	"encoding/csv"
	"strings"
	"sync"
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
	OtherCodes []string // the municipality's other 4-72 codes, urban first
}

// Map of main and frequent municipalities in Colombia (normalized key, no accents, lowercase).
// It gives their accented names and aliases; postal codes come from the 4-72 table first,
// and the ones here are only used when the table doesn't have the city (Bogotá is split by localidad).
var colombiaCities = map[string]GeoInfo{
	"quibdo":        {City: "Quibdó", Department: "Chocó", PostalCode: "270001"},
	"istmina":       {City: "Istmina", Department: "Chocó", PostalCode: "274010"},
	"tad":           {City: "Tadó", Department: "Chocó", PostalCode: "271070"},
	"tado":          {City: "Tadó", Department: "Chocó", PostalCode: "271070"},
	"condoto":       {City: "Condoto", Department: "Chocó", PostalCode: "273030"},
	"medellin":      {City: "Medellín", Department: "Antioquia", PostalCode: "050001"},
	"bello":         {City: "Bello", Department: "Antioquia", PostalCode: "051050"},
	"envigado":      {City: "Envigado", Department: "Antioquia", PostalCode: "055420"},
	"itagui":        {City: "Itagüí", Department: "Antioquia", PostalCode: "055410"},
	"rionegro":      {City: "Rionegro", Department: "Antioquia", PostalCode: "054040"},
	"apartado":      {City: "Apartadó", Department: "Antioquia", PostalCode: "057840"},
	"turbo":         {City: "Turbo", Department: "Antioquia", PostalCode: "057860"},
	"bogota":        {City: "Bogotá D.C.", Department: "Bogotá D.C.", PostalCode: "110001"},
	"bogota dc":     {City: "Bogotá D.C.", Department: "Bogotá D.C.", PostalCode: "110001"},
	"bogota d.c.":   {City: "Bogotá D.C.", Department: "Bogotá D.C.", PostalCode: "110001"},
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
	"tumaco":        {City: "Tumaco", Department: "Nariño", PostalCode: "528501"},
}

var colombiaDepartments = map[string]string{
	"amazonas":                   "Amazonas",
	"antioquia":                  "Antioquia",
	"arauca":                     "Arauca",
	"atlantico":                  "Atlántico",
	"bogota":                     "Bogotá D.C.",
	"bogota dc":                  "Bogotá D.C.",
	"bogota d.c.":                "Bogotá D.C.",
	"bolivar":                    "Bolívar",
	"boyaca":                     "Boyacá",
	"caldas":                     "Caldas",
	"caqueta":                    "Caquetá",
	"casanare":                   "Casanare",
	"cauca":                      "Cauca",
	"cesar":                      "Cesar",
	"choco":                      "Chocó",
	"cordoba":                    "Córdoba",
	"cundinamarca":               "Cundinamarca",
	"guainia":                    "Guainía",
	"guaviare":                   "Guaviare",
	"huila":                      "Huila",
	"la guajira":                 "La Guajira",
	"guajira":                    "La Guajira",
	"magdalena":                  "Magdalena",
	"meta":                       "Meta",
	"narino":                     "Nariño",
	"norte de santander":         "Norte de Santander",
	"putumayo":                   "Putumayo",
	"quindio":                    "Quindío",
	"risaralda":                  "Risaralda",
	"san andres":                 "San Andrés y Providencia",
	"archipielago de san andres": "San Andrés y Providencia",
	"santander":                  "Santander",
	"sucre":                      "Sucre",
	"tolima":                     "Tolima",
	"valle":                      "Valle del Cauca",
	"valle del cauca":            "Valle del Cauca",
	"vaupes":                     "Vaupés",
	"vichada":                    "Vichada",
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

// ResolveCityAndDept interprets texts like "QUIBDO CHOCO", "Medellín, Antioquia" or "Quibdó".
func ResolveCityAndDept(raw string, titleCaser cases.Caser) (city, dept, postal string) {
	normInput := normalizeKey(raw)
	if normInput == "" {
		return "", "", ""
	}

	// 1. Exact match with a known city
	if info, ok := colombiaCities[normInput]; ok {
		return info.City, info.Department, curatedPostal(normInput, info.Department, info)
	}

	// 2. Split if it comes as "CITY DEPARTMENT" (e.g. "quibdo choco" or "santa marta magdalena")
	// Try known department suffixes from longest to shortest
	for deptKey, deptProper := range colombiaDepartments {
		suffix := " " + deptKey
		if strings.HasSuffix(normInput, suffix) {
			cityPart := strings.TrimSpace(strings.TrimSuffix(normInput, suffix))
			if info, ok := colombiaCities[cityPart]; ok {
				return info.City, deptProper, curatedPostal(cityPart, deptProper, info)
			}
			if m, ok := lookupMunicipality(cityPart, deptProper); ok {
				return titleCaser.String(strings.ToLower(m.City)), deptProper, m.PostalCode
			}
			return titleCaser.String(cityPart), deptProper, ""
		}
	}

	// 3. Municipality from the 4-72 table, only if its name exists in a single department
	if m, ok := lookupMunicipality(normInput, ""); ok {
		return titleCaser.String(strings.ToLower(m.City)), m.Department, m.PostalCode
	}

	// 4. Fallback: capitalize whatever comes without inventing a department if it isn't recognized
	return titleCaser.String(strings.ToLower(strings.TrimSpace(raw))), "", ""
}

// LookupPostalCode tries to get a reference postal code if the user edited the city.
// dept may be empty; then a name repeated in several departments gives no code.
func LookupPostalCode(city, dept string) string {
	key := normalizeKey(city)
	if info, ok := colombiaCities[key]; ok {
		if dept == "" {
			dept = info.Department
		}
		return curatedPostal(key, dept, info)
	}
	if m, ok := lookupMunicipality(key, dept); ok {
		return m.PostalCode
	}
	return ""
}

// OtherPostalCodes returns the municipality's other 4-72 postal codes (urban first), to try when
// Skydropx doesn't accept the main one.
func OtherPostalCodes(city, dept string) []string {
	key := normalizeKey(city)
	if info, ok := colombiaCities[key]; ok {
		key = normalizeKey(info.City)
		if dept == "" {
			dept = info.Department
		}
	}
	if m, ok := lookupMunicipality(key, dept); ok {
		return m.OtherCodes
	}
	return nil
}

func curatedPostal(key, dept string, info GeoInfo) string {
	// The proper name also covers aliases like "tad" -> Tadó.
	for _, k := range []string{key, normalizeKey(info.City)} {
		if m, ok := lookupMunicipality(k, dept); ok {
			return m.PostalCode
		}
	}
	return info.PostalCode
}

// postal_codes.csv is the 4-72 national table (datos.gov.co, CC BY-SA 4.0), one urban code
// per municipality. Regenerate it with: go run ./cmd/postalcodes
//
//go:embed postal_codes.csv
var postalCodesCSV string

var (
	postalOnce    sync.Once
	postalByCity  map[string][]GeoInfo // normalized municipality -> one entry per department
	postalByAlias map[string][]GeoInfo // same without the leading article: "banco" -> El Banco
)

func loadPostalCodes() {
	postalByCity = map[string][]GeoInfo{}
	postalByAlias = map[string][]GeoInfo{}
	records, err := csv.NewReader(strings.NewReader(postalCodesCSV)).ReadAll()
	if err != nil {
		panic("postal_codes.csv: " + err.Error())
	}
	for _, r := range records[1:] {
		dept := colombiaDepartments[normalizeKey(r[0])]
		if dept == "" {
			dept = r[0]
		}
		info := GeoInfo{City: r[1], Department: dept, PostalCode: r[2], OtherCodes: strings.Fields(r[3])}
		key := normalizeKey(r[1])
		postalByCity[key] = append(postalByCity[key], info)
		for _, article := range []string{"el ", "la ", "los ", "las "} {
			if rest, ok := strings.CutPrefix(key, article); ok {
				postalByAlias[rest] = append(postalByAlias[rest], info)
			}
		}
	}
}

// lookupMunicipality finds the municipality in the 4-72 table. With an empty dept it only
// answers when the name belongs to a single department.
func lookupMunicipality(key, dept string) (GeoInfo, bool) {
	postalOnce.Do(loadPostalCodes)
	candidates := postalByCity[key]
	if len(candidates) == 0 {
		candidates = postalByAlias[key]
	}
	if dept == "" {
		if len(candidates) == 1 {
			return candidates[0], true
		}
		return GeoInfo{}, false
	}
	deptKey := normalizeKey(dept)
	if proper, ok := colombiaDepartments[deptKey]; ok {
		deptKey = normalizeKey(proper) // "Valle" -> "valle del cauca"
	}
	for _, c := range candidates {
		if normalizeKey(c.Department) == deptKey {
			return c, true
		}
	}
	return GeoInfo{}, false
}

// Interrapidísimo problems by municipality, found by quoting every municipality through Skydropx
// (go run ./cmd/validatepostal). Only municipalities where it doesn't work are listed.
//
//go:embed interrapidisimo_coverage.csv
var coverageCSV string

const (
	CoverageNoCOD  = "sin_contraentrega" // Interrapidísimo reaches it, but not with cash on delivery
	CoverageNone   = "sin_cobertura"     // Interrapidísimo doesn't reach it
	CoverageNoCode = "codigo_no_existe"  // Skydropx accepts none of its postal codes
)

var (
	coverageOnce sync.Once
	coverage     map[string]string // "department|municipality" (normalized) -> one of the Coverage* values
)

func loadCoverage() {
	coverage = map[string]string{}
	records, err := csv.NewReader(strings.NewReader(coverageCSV)).ReadAll()
	if err != nil {
		panic("interrapidisimo_coverage.csv: " + err.Error())
	}
	for _, r := range records[1:] {
		dept := colombiaDepartments[normalizeKey(r[0])]
		if dept == "" {
			dept = r[0]
		}
		coverage[normalizeKey(dept)+"|"+normalizeKey(r[1])] = r[2]
	}
}

// InterrapidisimoCoverage returns one of the Coverage* values when Interrapidísimo is known not to
// work for that municipality, or "" if it works or the municipality isn't recognized.
func InterrapidisimoCoverage(city, dept string) string {
	key := normalizeKey(city)
	if info, ok := colombiaCities[key]; ok {
		key = normalizeKey(info.City)
		if dept == "" {
			dept = info.Department
		}
	}
	m, ok := lookupMunicipality(key, dept)
	if !ok {
		return ""
	}
	coverageOnce.Do(loadCoverage)
	return coverage[normalizeKey(m.Department)+"|"+normalizeKey(m.City)]
}
