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

// TestSheetsRealLabels builds sheets with real labels. The labels have customer data and aren't
// in the repository: LABELS_DIR points to a folder with label_*.pdf and SHEETS_OUT to the output PDF.
func TestSheetsRealLabels(t *testing.T) {
	dir := os.Getenv("LABELS_DIR")
	if dir == "" {
		t.Skip("LABELS_DIR no está definido")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "label_*.pdf"))
	if len(files) == 0 {
		t.Fatalf("no hay label_*.pdf en %s", dir)
	}
	var lbls [][]byte
	for len(lbls) < 7 {
		for _, f := range files {
			if len(lbls) == 7 {
				break
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			lbls = append(lbls, b)
		}
	}
	out, err := Sheets(context.Background(), lbls)
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
