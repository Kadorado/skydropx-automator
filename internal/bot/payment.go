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

const helpPayment = `Para registrar lo que le pagaste al vendedor:
<code>/pago 150000</code> → marca como pagado lo cobrado, de lo más antiguo a lo más nuevo, hasta donde alcance
<code>/pago 150000 hasta ayer</code> → solo lo cobrado de guías creadas hasta ayer
<code>/pago 150000 hasta 2026-11-15</code> → solo guías creadas hasta ese día
<code>/pago borrar</code> → borra el último pago si te equivocaste
<code>/pagos</code> → ver los pagos registrados`

// paymentArgs parses "/pago 150000 [hasta ayer|hoy|AAAA-MM-DD]". Empty until = no date limit.
func paymentArgs(args string, now time.Time) (amt float64, until string, err error) {
	f := strings.Fields(strings.ToLower(args))
	if len(f) == 0 {
		return 0, "", errors.New("falta el valor")
	}
	amt, err = strconv.ParseFloat(strings.NewReplacer(".", "", "$", "", ",", "").Replace(f[0]), 64)
	if err != nil || amt <= 0 {
		return 0, "", fmt.Errorf("no entendí el valor %q", f[0])
	}
	f = f[1:]
	if len(f) > 0 && f[0] == "hasta" {
		f = f[1:]
	}
	switch {
	case len(f) == 0:
		return amt, "", nil
	case len(f) > 1:
		return 0, "", errors.New("demasiados datos")
	}
	today := now.In(colombiaTZ)
	switch f[0] {
	case "hoy":
		return amt, today.Format("2006-01-02"), nil
	case "ayer":
		return amt, today.AddDate(0, 0, -1).Format("2006-01-02"), nil
	}
	if _, err := time.ParseInLocation("2006-01-02", f[0], colombiaTZ); err != nil {
		return 0, "", fmt.Errorf("no entendí la fecha %q (usa AAAA-MM-DD, hoy o ayer)", f[0])
	}
	return amt, f[0], nil
}

// unpaidRows are the collected shipments not yet paid to the seller.
func unpaidRows(rows []skydropx.ShipmentMoney, paid map[string]string) []skydropx.ShipmentMoney {
	var out []skydropx.ShipmentMoney
	for _, r := range rows {
		if r.Status == "collected" && r.Tracking != "" && paid[r.Tracking] == "" {
			out = append(out, r)
		}
	}
	return out
}

// paymentCommand handles /pago: records what was paid to the seller and marks as paid the collected
// shipments it covers, so /deuda doesn't add them again.
func (b *Bot) paymentCommand(ctx context.Context, chatID int64, text string) {
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
				formatCOP(p.Amount), p.Date, orderCount(len(p.Trackings))))
		}
		return
	}
	amt, until, err := paymentArgs(args, time.Now())
	if err != nil {
		b.say(ctx, chatID, "No entendí: "+err.Error()+".\n\n"+helpPayment)
		return
	}
	b.say(ctx, chatID, "⏳ Buscando lo cobrado que no se le había pagado…")
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	p, err := skydropx.NewPeriod("", until)
	if err != nil {
		b.say(ctx, chatID, "No se pudo registrar: "+err.Error())
		return
	}
	f, err := b.sky.FullFinance(ctx, p, b.commissionRate())
	if err != nil {
		b.say(ctx, chatID, "No se pudo registrar: "+err.Error())
		return
	}
	balanceBefore := b.opts.Settings.VendorBalance()
	covered, remaining := allocatePayment(unpaidRows(f.PerShipment, b.opts.Settings.PaidTrackings()), amt-balanceBefore)
	payment := settings.VendorPayment{Date: time.Now().In(colombiaTZ).Format("2006-01-02"), Amount: amt, Through: until}
	for _, r := range covered {
		payment.Covered += r.Commission
		payment.Units += r.Units
		payment.Trackings = append(payment.Trackings, r.Tracking)
	}
	if err := b.opts.Settings.AddVendorPayment(payment); err != nil {
		b.say(ctx, chatID, "No se pudo guardar el pago: "+err.Error())
		return
	}
	msg := paymentText(payment, covered, remaining, balanceBefore)
	if f.Commission.Estimated > 0 {
		msg += fmt.Sprintf("\n⚠️ <i>No se pudo saber la cantidad de %d pedido(s) y se contaron como 1 datáfono. Si no cuadra, haz <code>/pago borrar</code> y vuelve a registrarlo en un rato.</i>\n", f.Commission.Estimated)
	}
	b.say(ctx, chatID, msg)
}

// allocatePayment marks collected shipments as paid from oldest to newest while the budget lasts
// (what was paid, plus what the seller owed or minus what was owed to them before). It stops at
// the first one that doesn't fit so the payment is always "up to such day"; the rest stay unpaid.
func allocatePayment(pendingRows []skydropx.ShipmentMoney, budget float64) (covered, remaining []skydropx.ShipmentMoney) {
	ord := append([]skydropx.ShipmentMoney(nil), pendingRows...)
	sort.SliceStable(ord, func(i, j int) bool { return shipDate(ord[i]).Before(shipDate(ord[j])) })
	var used float64
	for i, r := range ord {
		if used+r.Commission > budget+0.5 {
			return covered, ord[i:]
		}
		used += r.Commission
		covered = append(covered, r)
	}
	return covered, nil
}

// shipDate is the label's creation date (zero if it can't be parsed: it goes first).
func shipDate(r skydropx.ShipmentMoney) time.Time {
	t, _ := time.Parse(time.RFC3339, r.Date)
	return t
}

// shortDate shows "2026-11-16" as "lun 16 nov" (if it can't be parsed, leaves it as is).
func shortDate(s string) string {
	t, err := time.ParseInLocation("2006-01-02", s, colombiaTZ)
	if err != nil {
		return s
	}
	return shortDays[t.Weekday()] + " " + shortDay(t)
}

// byDay groups shipments by label day, in the order they come.
func byDay(rows []skydropx.ShipmentMoney) (lines []string, total amounts) {
	days := map[string]*amounts{}
	var ord []string
	for _, r := range rows {
		key := "sin fecha"
		if t := shipDate(r); !t.IsZero() {
			t = t.In(colombiaTZ)
			key = shortDays[t.Weekday()] + " " + shortDay(t)
		}
		if days[key] == nil {
			days[key] = &amounts{}
			ord = append(ord, key)
		}
		days[key].sumUp(r)
	}
	for _, k := range ord {
		lines = append(lines, fmt.Sprintf("• %s · %s · %s", k, days[k].account(), formatCOP(days[k].amt)))
		total.addAll(*days[k])
	}
	return lines, total
}

// paymentText confirms a recorded payment: which shipments it covers per day, which remain unpaid and how
// the balance with the seller ends up.
func paymentText(p settings.VendorPayment, covered, remaining []skydropx.ShipmentMoney, balanceBefore float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "💵 <b>PAGO AL VENDEDOR REGISTRADO</b>\n📅 %s · <b>%s</b>\n", shortDate(p.Date), formatCOP(p.Amount))
	if p.Through != "" {
		fmt.Fprintf(&b, "Solo guías creadas hasta el %s.\n", shortDate(p.Through))
	}
	switch {
	case balanceBefore > 0.5:
		fmt.Fprintf(&b, "↩️ Primero cubre %s que le debías de antes.\n", formatCOP(balanceBefore))
	case balanceBefore < -0.5:
		fmt.Fprintf(&b, "↩️ Se le descontaron %s que él te debía (publicidad o adelantos).\n", formatCOP(-balanceBefore))
	}
	if len(covered) > 0 {
		lines, total := byDay(covered)
		fmt.Fprintf(&b, "\n✔️ <b>QUEDA PAGADO: %s</b> · %s\n", formatCOP(total.amt), total.account())
		b.WriteString(strings.Join(lines, "\n") + "\n")
	} else if len(remaining) == 0 {
		b.WriteString("\nNo había envíos cobrados pendientes de pagarle: queda como adelanto.\n")
	} else {
		b.WriteString("\nNo alcanzó para cubrir ningún envío cobrado completo: queda como adelanto.\n")
	}
	if len(remaining) > 0 {
		lines, total := byDay(remaining)
		fmt.Fprintf(&b, "\n✅ <b>AÚN SE LE DEBE: %s</b> · %s ya cobrados\n", formatCOP(total.amt), total.account())
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}
	balance := balanceBefore + p.Covered - p.Amount
	if len(remaining) == 0 && balance > -0.5 && balance < 0.5 {
		b.WriteString("\n✅ Quedan a paz y salvo con lo cobrado.\n")
	} else if balance < -0.5 || balance > 0.5 {
		b.WriteString("\n")
	}
	b.WriteString(balanceText(balance))
	b.WriteString("\n<i>Si te equivocaste: /pago borrar</i>\n")
	return b.String()
}

// balanceText says how the account with the seller stands from payments and ads ("" if it is zero).
func balanceText(balance float64) string {
	switch {
	case balance > 0.5:
		return fmt.Sprintf("↪️ Contando pagos y publicidad, aún le debes <b>%s</b> aparte de lo cobrado.\n", formatCOP(balance))
	case balance < -0.5:
		return fmt.Sprintf("↪️ Contando pagos y publicidad, él te debe <b>%s</b>: descuéntalo de su próximo pago.\n", formatCOP(-balance))
	}
	return ""
}

// paymentsCommand handles /pagos: the latest payments to the seller and the balance.
func (b *Bot) paymentsCommand(ctx context.Context, chatID int64) {
	if b.opts.Settings == nil {
		b.say(ctx, chatID, "No hay archivo de ajustes configurado: no hay pagos guardados.")
		return
	}
	payments := b.opts.Settings.VendorPayments()
	if len(payments) == 0 {
		b.say(ctx, chatID, "No hay pagos registrados.\n\n"+helpPayment)
		return
	}
	var sb strings.Builder
	var totalPaid float64
	for _, p := range payments {
		totalPaid += p.Amount
	}
	fmt.Fprintf(&sb, "💵 <b>PAGOS AL VENDEDOR</b>\nTotal pagado: <b>%s</b> en %d pago(s)\n\n", formatCOP(totalPaid), len(payments))
	for i, n := len(payments)-1, 0; i >= 0 && n < 15; i, n = i-1, n+1 {
		p := payments[i]
		fmt.Fprintf(&sb, "📅 <b>%s</b> · %s\n", shortDate(p.Date), formatCOP(p.Amount))
		if len(p.Trackings) > 0 {
			fmt.Fprintf(&sb, "   cubrió %s · %s\n", amounts{orderCount: len(p.Trackings), unitCount: p.Units}.account(), formatCOP(p.Covered))
		} else {
			sb.WriteString("   adelanto, sin envíos cubiertos\n")
		}
	}
	if len(payments) > 15 {
		sb.WriteString("…\n")
	}
	if s := balanceText(b.opts.Settings.VendorBalance()); s != "" {
		sb.WriteString("\n" + s)
	} else {
		sb.WriteString("\n✅ Están a paz y salvo con pagos y publicidad.\n")
	}
	b.say(ctx, chatID, sb.String())
}
