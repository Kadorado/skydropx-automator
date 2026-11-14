// Package labels arma las guías de Skydropx en hojas carta de 4 guías para imprimir.
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

// PerSheet es cuántas guías caben en una hoja carta (2 x 2).
const PerSheet = 4

// La guía estándar de Interrapidísimo llega como hoja A4 horizontal (842 x 595) girada 90°,
// con la etiqueta en este rectángulo. Sin el giro y recortada, la etiqueta queda derecha.
var interLabelBox = types.NewRectangle(10, 52, 358, 584)

// Sheets une las guías (un PDF por guía) en un solo PDF con 4 guías por hoja carta.
// Si son 7 guías salen 2 hojas: una de 4 y otra de 3.
func Sheets(ctx context.Context, guias [][]byte) ([]byte, error) {
	if len(guias) == 0 {
		return nil, fmt.Errorf("no hay guías para armar")
	}
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	readers := make([]io.ReadSeeker, len(guias))
	for i, g := range guias {
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
		if err := recortarEtiqueta(ctx, pdf, p); err != nil {
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

// recortarEtiqueta deja solo la etiqueta derecha si la página tiene el formato de Interrapidísimo.
// Otros formatos se dejan tal cual y se ajustan completos a su cuarto de hoja.
func recortarEtiqueta(ctx context.Context, pdf *model.Context, page int) error {
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
