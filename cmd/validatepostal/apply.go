package main

import (
	"encoding/csv"
	"os"
	"slices"
	"strings"
)

const coveragePath = "internal/parser/interrapidisimo_coverage.csv"

// apply moves the validation results into the bot's tables: the code Skydropx accepted becomes the
// municipality's main code, and municipalities where Interrapidísimo doesn't work go to the
// coverage list. Codes accepted only as a neighboring number ("vecino") are left for manual review.
// Run it again after `go run ./cmd/postalcodes`, which rewrites the postal table from 4-72.
func apply(resultsPath string) (changed, problems int, err error) {
	results, err := readCSV(resultsPath)
	if err != nil {
		return 0, 0, err
	}
	last := map[string][]string{} // the last row of each municipality is the valid one
	for _, r := range results[1:] {
		last[r[0]+"|"+r[1]] = r
	}

	table, err := readCSV(tablePath)
	if err != nil {
		return 0, 0, err
	}
	for _, row := range table[1:] {
		r, ok := last[row[0]+"|"+row[1]]
		if !ok || r[4] != "corregido" || r[3] == row[2] {
			continue
		}
		// The accepted code goes first; the old main code joins the others.
		others := slices.DeleteFunc(strings.Fields(row[3]), func(c string) bool { return c == r[3] })
		row[2], row[3] = r[3], strings.Join(append([]string{row[2]}, others...), " ")
		changed++
	}
	if err := writeCSV(tablePath, table); err != nil {
		return 0, 0, err
	}

	coverage := [][]string{{"departamento", "municipio", "estado"}}
	for _, row := range table[1:] {
		r, ok := last[row[0]+"|"+row[1]]
		if !ok {
			continue
		}
		switch r[4] {
		case "sin_contraentrega", "sin_cobertura", "codigo_no_existe":
			coverage = append(coverage, []string{row[0], row[1], r[4]})
			problems++
		}
	}
	return changed, problems, writeCSV(coveragePath, coverage)
}

func writeCSV(path string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
