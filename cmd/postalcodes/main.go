// Command postalcodes downloads the 4-72 national postal code table from datos.gov.co
// and writes internal/parser/postal_codes.csv: one urban postal code per municipality.
//
// Usage: go run ./cmd/postalcodes
package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// "Códigos Postales Nacionales", published by Servicios Postales Nacionales (4-72), CC BY-SA 4.0.
const sourceURL = "https://www.datos.gov.co/resource/ixig-z8b5.json?$limit=10000&$select=nombre_departamento,nombre_municipio,codigo_postal,tipo"

const outPath = "internal/parser/postal_codes.csv"

type row struct {
	Department string `json:"nombre_departamento"`
	City       string `json:"nombre_municipio"`
	PostalCode string `json:"codigo_postal"`
	Type       string `json:"tipo"`
}

func main() {
	client := &http.Client{Timeout: time.Minute}
	resp, err := client.Get(sourceURL)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("datos.gov.co: %s", resp.Status)
	}
	var rows []row
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		log.Fatal(err)
	}

	// Keep the lowest urban code of each municipality; rural codes only when it has no urban one.
	type best struct {
		code  string
		urban bool
	}
	chosen := map[[2]string]best{}
	for _, r := range rows {
		code, err := fixPostalCode(r.PostalCode)
		if err != nil {
			log.Fatalf("%s, %s: %v", r.City, r.Department, err)
		}
		key := [2]string{strings.TrimSpace(r.Department), strings.TrimSpace(r.City)}
		urban := r.Type == "Urbano"
		cur, ok := chosen[key]
		if !ok || (urban && !cur.urban) || (urban == cur.urban && code < cur.code) {
			chosen[key] = best{code, urban}
		}
	}

	keys := make([][2]string, 0, len(chosen))
	for k := range chosen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	f, err := os.Create(outPath)
	if err != nil {
		log.Fatal(err)
	}
	w := csv.NewWriter(f)
	w.Write([]string{"departamento", "municipio", "codigo_postal"})
	for _, k := range keys {
		w.Write([]string{k[0], k[1], chosen[k].code})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d municipios escritos en %s\n", len(keys), outPath)
}

// fixPostalCode undoes the portal's numeric formatting: "473.04" -> "473040", "55.03" -> "055030".
func fixPostalCode(s string) (string, error) {
	whole, frac, ok := strings.Cut(strings.TrimSpace(s), ".")
	if !ok || len(frac) > 3 {
		return "", fmt.Errorf("código postal inesperado %q", s)
	}
	n, err := strconv.Atoi(whole + (frac + "000")[:3])
	if err != nil {
		return "", fmt.Errorf("código postal inesperado %q", s)
	}
	return fmt.Sprintf("%06d", n), nil
}
