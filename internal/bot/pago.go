package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
)

const ayudaPago = `Para registrar lo que le pagaste al vendedor:
<code>/pago 150000</code> → cubre todo lo cobrado que no se le había pagado
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
	cubre := porPagar(f.PerShipment, b.opts.Settings.PaidTrackings())
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
	msg := pagoTexto(pago, cubre, saldoAntes)
	if f.Commission.Estimated > 0 {
		msg += fmt.Sprintf("\n⚠️ <i>No se pudo saber la cantidad de %d pedido(s) y se contaron como 1 datáfono. Si no cuadra, haz <code>/pago borrar</code> y vuelve a registrarlo en un rato.</i>\n", f.Commission.Estimated)
	}
	b.say(ctx, chatID, msg)
}

// pagoTexto confirma un pago registrado: qué envíos cubre por día y cómo queda el saldo con el vendedor.
func pagoTexto(p settings.VendorPayment, cubre []skydropx.ShipmentMoney, saldoAntes float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "💵 <b>Pago al vendedor registrado: %s</b> (%s)\n", formatCOP(p.Amount), p.Date)
	if p.Through != "" {
		fmt.Fprintf(&b, "Cubre lo cobrado de guías creadas hasta el %s.\n", p.Through)
	}
	if len(cubre) == 0 {
		b.WriteString("\nNo había envíos cobrados pendientes de pagarle: queda como adelanto.\n")
	} else {
		dias := map[string]*monto{}
		var orden []string
		for _, r := range cubre {
			t, err := time.Parse(time.RFC3339, r.Date)
			key := "sin fecha"
			if err == nil {
				t = t.In(colombiaTZ)
				key = diasCortos[t.Weekday()] + " " + diaCorto(t)
			}
			if dias[key] == nil {
				dias[key] = &monto{}
				orden = append(orden, key)
			}
			dias[key].sumar(r)
		}
		var total monto
		b.WriteString("\n<b>Envíos cobrados que cubre</b> (por día de la guía)\n")
		for _, k := range orden {
			fmt.Fprintf(&b, "• %s · %s · %s\n", k, dias[k].cuenta(), formatCOP(dias[k].valor))
			total.juntar(*dias[k])
		}
		fmt.Fprintf(&b, "<b>Comisión de esos envíos: %s</b> (%s)\n", formatCOP(total.valor), total.cuenta())
	}

	diff := p.Covered - p.Amount
	saldo := saldoAntes + diff
	switch {
	case diff > 0.5:
		fmt.Fprintf(&b, "\n⚠️ Le pagaste %s menos de lo que suman.\n", formatCOP(diff))
	case diff < -0.5:
		fmt.Fprintf(&b, "\nℹ️ Le pagaste %s más de lo que suman.\n", formatCOP(-diff))
	default:
		b.WriteString("\n✅ Cuadra exacto.\n")
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
	sb.WriteString("💵 <b>Pagos al vendedor</b> (los más recientes primero)\n")
	for i, n := len(pagos)-1, 0; i >= 0 && n < 15; i, n = i-1, n+1 {
		p := pagos[i]
		fmt.Fprintf(&sb, "• %s: <b>%s</b> · cubre %s · %s", p.Date, formatCOP(p.Amount),
			monto{pedidos: len(p.Trackings), unidades: p.Units}.cuenta(), formatCOP(p.Covered))
		if p.Through != "" {
			fmt.Fprintf(&sb, " (guías hasta %s)", p.Through)
		}
		sb.WriteString("\n")
	}
	if s := saldoTexto(b.opts.Settings.VendorBalance()); s != "" {
		sb.WriteString("\n" + s)
	} else {
		sb.WriteString("\n✅ Están a paz y salvo con pagos y publicidad.\n")
	}
	b.say(ctx, chatID, sb.String())
}
