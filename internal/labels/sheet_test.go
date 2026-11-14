package labels

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"bytes"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// TestSheetsGuiasReales arma hojas con guías reales. Las guías tienen datos de clientes y no van
// en el repositorio: LABELS_DIR apunta a una carpeta con label_*.pdf y SHEETS_OUT al PDF de salida.
func TestSheetsGuiasReales(t *testing.T) {
	dir := os.Getenv("LABELS_DIR")
	if dir == "" {
		t.Skip("LABELS_DIR no está definido")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "label_*.pdf"))
	if len(files) == 0 {
		t.Fatalf("no hay label_*.pdf en %s", dir)
	}
	var guias [][]byte
	for len(guias) < 7 {
		for _, f := range files {
			if len(guias) == 7 {
				break
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			guias = append(guias, b)
		}
	}
	out, err := Sheets(context.Background(), guias)
	if err != nil {
		t.Fatal(err)
	}
	if p := os.Getenv("SHEETS_OUT"); p != "" {
		_ = os.WriteFile(p, out, 0o600)
	}
	n, err := api.PageCount(context.Background(), bytes.NewReader(out), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("7 guías deberían dar 2 hojas, salieron %d", n)
	}
}
