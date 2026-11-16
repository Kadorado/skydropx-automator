package bot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
)

const ayudaPago = `Para registrar lo que le pagaste al vendedor:
<code>/pago 150000</code> → marca como pagado lo cobrado, de lo más antiguo a lo más nuevo, hasta donde alcance
<code>/pago 150000 hasta ayer</code> → solo lo cobrado de guías creadas hasta ayer
<code>/pago 150000 hasta 2026-11-15</code> → solo guías creadas hasta ese día
<code>/pago borrar</code> → borra el último pago si te equivocaste
<code>/pagos</code> → ver los pagos registrados`

// argsPago entiende "/pago 150000 [hasta ayer|hoy|AAAA-MM-DD]". hasta vacío = sin límite de fecha.
func argsPago(args string, now time.Time) (valor float64, hasta string, err error) {
	f := strings.Fields(strings.ToLower(args))
	if len(f) == 0 {
		return 0, "", errors.New("falta el valor")
	}
	valor, err = strconv.ParseFloat(strings.NewReplacer(".", "", "$", "", ",", "").Replace(f[0]), 64)
	if err != nil || valor <= 0 {
		return 0, "", fmt.Errorf("no entendí el valor %q", f[0])
	}
	f = f[1:]
	if len(f) > 0 && f[0] == "hasta" {
		f = f[1:]
	}
	switch {
	case len(f) == 0:
		return valor, "", nil
	case len(f) > 1:
		return 0, "", errors.New("demasiados datos")
	}
	hoy := now.In(colombiaTZ)
	switch f[0] {
	case "hoy":
		return valor, hoy.Format("2006-01-02"), nil
	case "ayer":
		return valor, hoy.AddDate(0, 0, -1).Format("2006-01-02"), nil
	}
	if _, err := time.ParseInLocation("2006-01-02", f[0], colombiaTZ); err != nil {
		return 0, "", fmt.Errorf("no entendí la fecha %q (usa AAAA-MM-DD, hoy o ayer)", f[0])
	}
	return valor, f[0], nil
}

// porPagar son los envíos cobrados que todavía no se le pagaron al vendedor.
func porPagar(rows []skydropx.ShipmentMoney, pagadas map[string]string) []skydropx.ShipmentMoney {
	var out []skydropx.ShipmentMoney
	for _, r := range rows {
		if r.Status == "collected" && r.Tracking != "" && pagadas[r.Tracking] == "" {
			out = append(out, r)
		}
	}
	return out
}

// comandoPago atiende /pago: registra lo que se le pagó al vendedor y marca como pagados los envíos
// cobrados que cubre, para que /deuda no los vuelva a sumar.
func (b *Bot) comandoPago(ctx context.Context, chatID int64, text string) {
	if b.opts.Settings == nil {
		b.say(ctx, chatID, "No hay archivo de ajustes configurado: no se pueden guardar pagos.")
		return
	}
	args := strings.TrimSpace(strings.TrimPrefix(text, "/pago"))
	if strings.EqualFold(args, "borrar") {
		p, ok, err := b.opts.Settings.RemoveLastVendorPayment()
		switch {
		case err != nil:
			b.say(ctx, chatID, "No se pudo borrar: "+err.Error())
		case !ok:
			b.say(ctx, chatID, "No hay pagos registrados.")
		default:
			b.say(ctx, chatID, fmt.Sprintf("🗑️ Borré el pago de %s del %s. Sus %s vuelven a quedar por pagarle.",
				formatCOP(p.Amount), p.Date, pedidos(len(p.Trackings))))
		}
		return
	}
	valor, hasta, err := argsPago(args, time.Now())
	if err != nil {
		b.say(ctx, chatID, "No entendí: "+err.Error()+".\n\n"+ayudaPago)
		return
	}
	b.say(ctx, chatID, "⏳ Buscando lo cobrado que no se le había pagado…")
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	p, err := skydropx.NewPeriod("", hasta)
	if err != nil {
		b.say(ctx, chatID, "No se pudo registrar: "+err.Error())
		return
	}
	f, err := b.sky.FinanceCompleta(ctx, p, b.comisionRate())
	if err != nil {
		b.say(ctx, chatID, "No se pudo registrar: "+err.Error())
		return
	}
	saldoAntes := b.opts.Settings.VendorBalance()
	cubre, quedan := repartirPago(porPagar(f.PerShipment, b.opts.Settings.PaidTrackings()), valor-saldoAntes)
	pago := settings.VendorPayment{Date: time.Now().In(colombiaTZ).Format("2006-01-02"), Amount: valor, Through: hasta}
	for _, r := range cubre {
		pago.Covered += r.Commission
		pago.Units += r.Units
		pago.Trackings = append(pago.Trackings, r.Tracking)
	}
	if err := b.opts.Settings.AddVendorPayment(pago); err != nil {
		b.say(ctx, chatID, "No se pudo guardar el pago: "+err.Error())
		return
	}
	msg := pagoTexto(pago, cubre, quedan, saldoAntes)
	if f.Commission.Estimated > 0 {
		msg += fmt.Sprintf("\n⚠️ <i>No se pudo saber la cantidad de %d pedido(s) y se contaron como 1 datáfono. Si no cuadra, haz <code>/pago borrar</code> y vuelve a registrarlo en un rato.</i>\n", f.Commission.Estimated)
	}
	b.say(ctx, chatID, msg)
}

// repartirPago marca como pagados los envíos cobrados del más antiguo al más nuevo mientras alcance el
// presupuesto (lo pagado, más lo que el vendedor debía o menos lo que se le debía de antes). Se detiene en
// el primero que no alcanza para que lo pagado sea siempre "hasta tal día"; los demás quedan por pagarle.
func repartirPago(pendientes []skydropx.ShipmentMoney, presupuesto float64) (cubre, quedan []skydropx.ShipmentMoney) {
	orden := append([]skydropx.ShipmentMoney(nil), pendientes...)
	sort.SliceStable(orden, func(i, j int) bool { return fechaEnvio(orden[i]).Before(fechaEnvio(orden[j])) })
	var usado float64
	for i, r := range orden {
		if usado+r.Commission > presupuesto+0.5 {
			return cubre, orden[i:]
		}
		usado += r.Commission
		cubre = append(cubre, r)
	}
	return cubre, nil
}

// fechaEnvio es la fecha de creación de la guía (cero si no se entiende: va primero).
func fechaEnvio(r skydropx.ShipmentMoney) time.Time {
	t, _ := time.Parse(time.RFC3339, r.Date)
	return t
}

// fechaCorta muestra "2026-11-16" como "lun 16 nov" (si no se entiende, la deja igual).
func fechaCorta(s string) string {
	t, err := time.ParseInLocation("2006-01-02", s, colombiaTZ)
	if err != nil {
		return s
	}
	return diasCortos[t.Weekday()] + " " + diaCorto(t)
}

// porDia resume envíos por día de la guía, en el orden en que vienen.
func porDia(rows []skydropx.ShipmentMoney) (lineas []string, total monto) {
	dias := map[string]*monto{}
	var orden []string
	for _, r := range rows {
		key := "sin fecha"
		if t := fechaEnvio(r); !t.IsZero() {
			t = t.In(colombiaTZ)
			key = diasCortos[t.Weekday()] + " " + diaCorto(t)
		}
		if dias[key] == nil {
			dias[key] = &monto{}
			orden = append(orden, key)
		}
		dias[key].sumar(r)
	}
	for _, k := range orden {
		lineas = append(lineas, fmt.Sprintf("• %s · %s · %s", k, dias[k].cuenta(), formatCOP(dias[k].valor)))
		total.juntar(*dias[k])
	}
	return lineas, total
}

// pagoTexto confirma un pago registrado: qué envíos cubre por día, cuáles quedan por pagarle y cómo
// queda el saldo con el vendedor.
func pagoTexto(p settings.VendorPayment, cubre, quedan []skydropx.ShipmentMoney, saldoAntes float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "💵 <b>PAGO AL VENDEDOR REGISTRADO</b>\n📅 %s · <b>%s</b>\n", fechaCorta(p.Date), formatCOP(p.Amount))
	if p.Through != "" {
		fmt.Fprintf(&b, "Solo guías creadas hasta el %s.\n", fechaCorta(p.Through))
	}
	switch {
	case saldoAntes > 0.5:
		fmt.Fprintf(&b, "↩️ Primero cubre %s que le debías de antes.\n", formatCOP(saldoAntes))
	case saldoAntes < -0.5:
		fmt.Fprintf(&b, "↩️ Se le descontaron %s que él te debía (publicidad o adelantos).\n", formatCOP(-saldoAntes))
	}
	if len(cubre) > 0 {
		lineas, total := porDia(cubre)
		fmt.Fprintf(&b, "\n✔️ <b>QUEDA PAGADO: %s</b> · %s\n", formatCOP(total.valor), total.cuenta())
		b.WriteString(strings.Join(lineas, "\n") + "\n")
	} else if len(quedan) == 0 {
		b.WriteString("\nNo había envíos cobrados pendientes de pagarle: queda como adelanto.\n")
	} else {
		b.WriteString("\nNo alcanzó para cubrir ningún envío cobrado completo: queda como adelanto.\n")
	}
	if len(quedan) > 0 {
		lineas, total := porDia(quedan)
		fmt.Fprintf(&b, "\n✅ <b>AÚN SE LE DEBE: %s</b> · %s ya cobrados\n", formatCOP(total.valor), total.cuenta())
		b.WriteString(strings.Join(lineas, "\n") + "\n")
	}
	saldo := saldoAntes + p.Covered - p.Amount
	if len(quedan) == 0 && saldo > -0.5 && saldo < 0.5 {
		b.WriteString("\n✅ Quedan a paz y salvo con lo cobrado.\n")
	} else if saldo < -0.5 || saldo > 0.5 {
		b.WriteString("\n")
	}
	b.WriteString(saldoTexto(saldo))
	b.WriteString("\n<i>Si te equivocaste: /pago borrar</i>\n")
	return b.String()
}

// saldoTexto dice cómo queda la cuenta con el vendedor por pagos y publicidad ("" si está en cero).
func saldoTexto(saldo float64) string {
	switch {
	case saldo > 0.5:
		return fmt.Sprintf("↪️ Contando pagos y publicidad, aún le debes <b>%s</b> aparte de lo cobrado.\n", formatCOP(saldo))
	case saldo < -0.5:
		return fmt.Sprintf("↪️ Contando pagos y publicidad, él te debe <b>%s</b>: descuéntalo de su próximo pago.\n", formatCOP(-saldo))
	}
	return ""
}

// comandoPagos atiende /pagos: los últimos pagos al vendedor y el saldo.
func (b *Bot) comandoPagos(ctx context.Context, chatID int64) {
	if b.opts.Settings == nil {
		b.say(ctx, chatID, "No hay archivo de ajustes configurado: no hay pagos guardados.")
		return
	}
	pagos := b.opts.Settings.VendorPayments()
	if len(pagos) == 0 {
		b.say(ctx, chatID, "No hay pagos registrados.\n\n"+ayudaPago)
		return
	}
	var sb strings.Builder
	var totalPagado float64
	for _, p := range pagos {
		totalPagado += p.Amount
	}
	fmt.Fprintf(&sb, "💵 <b>PAGOS AL VENDEDOR</b>\nTotal pagado: <b>%s</b> en %d pago(s)\n\n", formatCOP(totalPagado), len(pagos))
	for i, n := len(pagos)-1, 0; i >= 0 && n < 15; i, n = i-1, n+1 {
		p := pagos[i]
		fmt.Fprintf(&sb, "📅 <b>%s</b> · %s\n", fechaCorta(p.Date), formatCOP(p.Amount))
		if len(p.Trackings) > 0 {
			fmt.Fprintf(&sb, "   cubrió %s · %s\n", monto{pedidos: len(p.Trackings), unidades: p.Units}.cuenta(), formatCOP(p.Covered))
		} else {
			sb.WriteString("   adelanto, sin envíos cubiertos\n")
		}
	}
	if len(pagos) > 15 {
		sb.WriteString("…\n")
	}
	if s := saldoTexto(b.opts.Settings.VendorBalance()); s != "" {
		sb.WriteString("\n" + s)
	} else {
		sb.WriteString("\n✅ Están a paz y salvo con pagos y publicidad.\n")
	}
	b.say(ctx, chatID, sb.String())
}
