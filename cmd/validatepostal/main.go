// Command validatepostal quotes Interrapidísimo through Skydropx for every municipality of
// internal/parser/postal_codes.csv and writes which postal code Skydropx accepts. It creates no
// orders or labels. It needs the same SKYDROPX_* variables as the server (with SKYDROPX_MODE=live).
//
// Usage: go run ./cmd/validatepostal -out results.csv [-dept Chocó]
// Then: go run ./cmd/validatepostal -out results.csv -apply (updates the bot's tables, no API calls).
// Rows already in -out are skipped (except errors, which are retried), so an interrupted run can be
// resumed; the last row of each municipality is the valid one. It shares the API rate limit with the
// bot, so it goes slowly (-pause) and waits when Skydropx says the limit was reached.
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"skydropx-automator/internal/config"
	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/parser"
	"skydropx-automator/internal/skydropx"
)

const tablePath = "internal/parser/postal_codes.csv"

func main() {
	out := flag.String("out", "postal_validation.csv", "results file")
	dept := flag.String("dept", "", "only this department (as written in the table, e.g. CHOCO)")
	pause := flag.Duration("pause", 4*time.Second, "pause between municipalities")
	applyResults := flag.Bool("apply", false, "move the results in -out into the bot's tables and exit")
	flag.Parse()

	if *applyResults {
		changed, problems, err := apply(*out)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%d códigos principales cambiados, %d municipios con problemas en la lista de cobertura\n", changed, problems)
		return
	}

	cfg := config.Load()
	if cfg.SkydropxMode == "mock" || cfg.SkydropxClientID == "" || cfg.SkydropxClientSecret == "" {
		log.Fatal("faltan SKYDROPX_CLIENT_ID / SKYDROPX_CLIENT_SECRET o SKYDROPX_MODE=live")
	}
	client := skydropx.NewClient(cfg)

	rows, err := readCSV(tablePath)
	if err != nil {
		log.Fatal(err)
	}
	done := map[string]bool{}
	prev, err := readCSV(*out)
	needHeader := err != nil || len(prev) == 0
	if !needHeader {
		for _, r := range prev[1:] {
			// errors and changed codes are retried (the retry order changed: own codes first)
			done[r[0]+"|"+r[1]] = r[4] != "error" && r[4] != "corregido" && r[4] != "codigo_no_existe"
		}
	}

	f, err := os.OpenFile(*out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if needHeader {
		w.Write([]string{"departamento", "municipio", "codigo_4_72", "codigo_aceptado", "estado", "tarifa", "dias", "mensaje"})
	}

	todo := 0
	for _, r := range rows[1:] {
		if (*dept == "" || strings.EqualFold(r[0], *dept)) && !done[r[0]+"|"+r[1]] {
			todo++
		}
	}
	n := 0
	for _, r := range rows[1:] {
		department, city, code := r[0], r[1], r[2]
		if (*dept != "" && !strings.EqualFold(department, *dept)) || done[department+"|"+city] {
			continue
		}
		n++
		res := validate(client, cfg, department, city, code)
		for try := 0; try < 3 && strings.Contains(res[4], "Límite de solicitudes"); try++ {
			log.Printf("límite de solicitudes de Skydropx: espero 2 minutos")
			time.Sleep(2 * time.Minute)
			res = validate(client, cfg, department, city, code)
		}
		w.Write(append([]string{department, city, code}, res...))
		w.Flush()
		log.Printf("[%d/%d] %s, %s %s -> %s %s", n, todo, city, department, code, res[1], res[0])
		time.Sleep(*pause)
	}
	if err := w.Error(); err != nil {
		log.Fatal(err)
	}
}

// validate returns codigo_aceptado, estado, tarifa, dias, mensaje.
func validate(client *skydropx.Client, cfg config.Config, department, city, code string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	q, err := client.QuoteLabel(ctx, domain.ShipmentData{
		Name: "Prueba", City: city, Department: department, PostalCode: code,
		Quantity: 1, UnitPrice: cfg.DefaultUnitPrice,
	})
	var state string
	switch {
	case err == nil && q.PostalCode == code:
		state = "ok"
	case err == nil && slices.Contains(parser.OtherPostalCodes(city, department), q.PostalCode):
		state = "corregido" // another 4-72 code of the same municipality
	case err == nil:
		state = "vecino" // a neighboring number: it may belong to another municipality
	case errors.Is(err, context.DeadlineExceeded):
		state = "error"
	case strings.Contains(err.Error(), "contra pago"):
		state = "sin_contraentrega" // the carrier reaches it, but not cash on delivery
	case strings.Contains(err.Error(), "no reconoce el código postal"):
		state = "codigo_no_existe"
	case strings.Contains(err.Error(), "cobertura") || strings.Contains(err.Error(), "Interrapid"):
		state = "sin_cobertura"
	default:
		state = "error"
	}
	if err != nil {
		return []string{"", state, "", "", strings.TrimSpace(err.Error())}
	}
	return []string{q.PostalCode, state, fmt.Sprintf("%.0f", q.Total), fmt.Sprint(q.Days), q.Service}
}

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return csv.NewReader(f).ReadAll()
}
