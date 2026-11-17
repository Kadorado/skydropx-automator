// Package labels lays out Skydropx labels on letter sheets of 4 labels for printing.
package labels

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// PerSheet is how many labels fit on a letter sheet (2 x 2).
const PerSheet = 4

// The standard Interrapidísimo label comes as a landscape A4 sheet (842 x 595) rotated 90°,
// with the label in this rectangle. Unrotated and cropped, the label ends up upright.
var interLabelBox = types.NewRectangle(10, 52, 358, 584)

// Sheets merges the labels (one PDF per label) into a single PDF with 4 labels per letter sheet.
// 7 labels make 2 sheets: one of 4 and one of 3.
func Sheets(ctx context.Context, lbls [][]byte) ([]byte, error) {
	if len(lbls) == 0 {
		return nil, fmt.Errorf("no hay guías para armar")
	}
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	readers := make([]io.ReadSeeker, len(lbls))
	for i, g := range lbls {
		readers[i] = bytes.NewReader(g)
	}
	var merged bytes.Buffer
	if err := api.MergeRaw(ctx, readers, &merged, false, conf); err != nil {
		return nil, fmt.Errorf("no se pudieron unir las guías: %w", err)
	}

	pdf, err := api.ReadAndValidate(ctx, bytes.NewReader(merged.Bytes()), conf)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer las guías: %w", err)
	}
	if err := pdf.EnsurePageCount(); err != nil {
		return nil, err
	}
	for p := 1; p <= pdf.PageCount; p++ {
		if err := cropLabel(ctx, pdf, p); err != nil {
			return nil, err
		}
	}

	var cropped bytes.Buffer
	if err := api.WriteContext(ctx, pdf, &cropped); err != nil {
		return nil, err
	}

	nup, err := api.PDFNUpConfig(PerSheet, "formsize:Letter, border:off, margin:0", conf)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := api.NUp(ctx, bytes.NewReader(cropped.Bytes()), &out, nil, nil, nup, conf); err != nil {
		return nil, fmt.Errorf("no se pudieron armar las hojas: %w", err)
	}
	return out.Bytes(), nil
}

// cropLabel keeps only the upright label if the page has the Interrapidísimo format.
// Other formats are left as is and fitted whole into their quarter of the sheet.
func cropLabel(ctx context.Context, pdf *model.Context, page int) error {
	dict, _, inh, err := pdf.PageDict(ctx, page, false)
	if err != nil {
		return err
	}
	mb := inh.MediaBox
	if mb == nil || int(mb.Width()) != 842 || int(mb.Height()) != 595 || (inh.Rotate != 90 && inh.Rotate != -270) {
		return nil
	}
	dict.Update("CropBox", interLabelBox.Array())
	dict.Update("Rotate", types.Integer(0))
	return nil
}
