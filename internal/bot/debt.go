package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
)

// maxDebtDays limits the /deuda range so the message fits in Telegram.
const maxDebtDays = 62

var shortDays = [...]string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}

// debtRange parses the /deuda range (dates in Colombia time, both inclusive):
// empty = last 7 days, "5" = last 5 days, "hoy", "ayer", "semana" = the previous one (Monday to Sunday),
// "mes" = the current month, "2026-11-03" = that day, "2026-11-01 2026-11-10" = that range.
func debtRange(args string, now time.Time) (from, until time.Time, err error) {
	now = now.In(colombiaTZ)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, colombiaTZ)
	f := strings.Fields(strings.ToLower(args))
	switch {
	case len(f) == 0:
		return today.AddDate(0, 0, -6), today, nil
	case len(f) == 1 && f[0] == "hoy":
		return today, today, nil
	case len(f) == 1 && f[0] == "ayer":
		return today.AddDate(0, 0, -1), today.AddDate(0, 0, -1), nil
	case len(f) == 1 && f[0] == "semana":
		offset := (int(today.Weekday()) + 6) % 7 // days since Monday
		weekStart := today.AddDate(0, 0, -offset-7)
		return weekStart, weekStart.AddDate(0, 0, 6), nil
	case len(f) == 1 && f[0] == "mes":
		return today.AddDate(0, 0, 1-today.Day()), today, nil
	case len(f) == 1:
		if n, err := strconv.Atoi(f[0]); err == nil {
			if n < 1 || n > maxDebtDays {
				return from, until, fmt.Errorf("los días van de 1 a %d", maxDebtDays)
			}
			return today.AddDate(0, 0, 1-n), today, nil
		}
		d, err := time.ParseInLocation("2006-01-02", f[0], colombiaTZ)
		if err != nil {
			return from, until, fmt.Errorf("no entendí %q", f[0])
		}
		return d, d, nil
	case len(f) == 2:
		d, err1 := time.ParseInLocation("2006-01-02", f[0], colombiaTZ)
		h, err2 := time.ParseInLocation("2006-01-02", f[1], colombiaTZ)
		if err1 != nil || err2 != nil {
			return from, until, errors.New("las fechas van como AAAA-MM-DD")
		}
		if d.After(h) {
			d, h = h, d
		}
		if h.Sub(d) >= maxDebtDays*24*time.Hour {
			return from, until, fmt.Errorf("el rango puede ser de hasta %d días", maxDebtDays)
		}
		return d, h, nil
	}
	return from, until, errors.New("demasiados datos")
}

// debtCommand handles /deuda: what is owed to the seller over a range of days, day by day.
func (b *Bot) debtCommand(ctx context.Context, chatID int64, text string) {
	from, until, err := debtRange(strings.TrimPrefix(text, "/deuda"), time.Now())
	if err != nil {
		b.say(ctx, chatID, "No entendí el rango: "+err.Error()+".\n\n"+helpDebt)
		return
	}
	b.say(ctx, chatID, "⏳ Sumando la comisión y revisando el rastreo de lo que falta por cobrar…")
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	p, err := skydropx.NewPeriod(from.Format("2006-01-02"), until.Format("2006-01-02"))
	if err != nil {
		b.say(ctx, chatID, "No se pudo armar el resumen: "+err.Error())
		return
	}
	f, err := b.sky.FullFinance(ctx, p, b.commissionRate())
	if err != nil {
		b.say(ctx, chatID, "No se pudo armar el resumen: "+err.Error())
		return
	}
	states := map[string]string{} // label -> inTransit | office | issue
	for _, r := range f.PerShipment {
		if r.Status == "pending" {
			states[r.Tracking] = b.sky.PendingCODState(ctx, r.Tracking, r.Carrier)
		}
	}
	var c sellerAccount
	if b.opts.Settings != nil {
		c = sellerAccount{b.opts.Settings.PaidTrackings(), b.opts.Settings.AdExpenses(), b.opts.Settings.VendorBalance()}
	}
	b.say(ctx, chatID, debtText(from, until, f, states, c))
}

const helpDebt = `Ejemplos:
<code>/deuda</code> → últimos 7 días
<code>/deuda 3</code> → últimos 3 días
<code>/deuda hoy</code> · <code>/deuda ayer</code>
<code>/deuda semana</code> → la semana anterior (lunes a domingo)
<code>/deuda mes</code> → lo que va del mes
<code>/deuda 2026-11-03</code> → ese día
<code>/deuda 2026-11-01 2026-11-10</code> → ese rango`

// amounts are the orders, their terminals and the commission they add up to.
type amounts struct {
	orderCount, unitCount int
	amt                   float64
}

func (m *amounts) sumUp(r skydropx.ShipmentMoney) {
	m.orderCount++
	m.unitCount += r.Units
	m.amt += r.Commission
}

func (m *amounts) addAll(o amounts) {
	m.orderCount += o.orderCount
	m.unitCount += o.unitCount
	m.amt += o.amt
}

// account says how many orders and terminals there are, without repeating when each order is one terminal.
func (m amounts) account() string {
	if m.orderCount == m.unitCount {
		return terminals(m.unitCount)
	}
	return orderCount(m.orderCount) + " = " + terminals(m.unitCount)
}

// sellerAccount is what was recorded by hand with the seller: payments, shared ads and the balance between both.
type sellerAccount struct {
	paid    map[string]string // label -> day it was paid
	ads     []settings.AdExpense
	balance float64
}

// debtText builds the /deuda message in three blocks: how much to pay today (what was collected and not paid plus
// the balance of payments and ads), the summary by state and the day by day with the orders of several
// terminals. Sales count on the day the label was generated; shipments to collect are split into
// in transit, office or issue according to states.
func debtText(from, until time.Time, f skydropx.Finance, states map[string]string, c sellerAccount) string {
	type day struct {
		settled, paidOut, inTransit, office, issue amounts
		failedOnes                                 int
		several                                    []string // orders of more than one terminal
	}
	icons := map[string]string{"liquidado": "💵", "pagado": "✅", "camino": "🚚", "oficina": "🏢", "novedad": "⚠️"}
	days := map[string]*day{}
	var total day
	for _, r := range f.PerShipment {
		t, err := time.Parse(time.RFC3339, r.Date)
		if err != nil {
			continue
		}
		key := t.In(colombiaTZ).Format("2006-01-02")
		d := days[key]
		if d == nil {
			d = &day{}
			days[key] = d
		}
		stage := ""
		switch r.Status {
		case "collected":
			stage = "pagado"
			if c.paid[r.Tracking] != "" {
				stage = "liquidado"
			}
		case "pending":
			stage = "camino"
			if e := states[r.Tracking]; e == "oficina" || e == "novedad" {
				stage = e
			}
		case "failed":
			d.failedOnes++
			total.failedOnes++
		}
		if stage == "" {
			continue
		}
		for _, x := range []*day{d, &total} {
			map[string]*amounts{"liquidado": &x.settled, "pagado": &x.paidOut, "camino": &x.inTransit,
				"oficina": &x.office, "novedad": &x.issue}[stage].sumUp(r)
		}
		if r.Units > 1 {
			cust := strings.TrimSpace(r.Customer)
			if cust == "" {
				cust = "Guía " + r.Tracking
			}
			d.several = append(d.several, fmt.Sprintf("📦 %s: %s × %s %s", html.EscapeString(cust),
				terminals(r.Units), formatCOP(r.Commission/float64(r.Units)), icons[stage]))
		}
	}

	var toCollect, earned amounts
	for _, m := range []amounts{total.inTransit, total.office, total.issue} {
		toCollect.addAll(m)
	}
	for _, m := range []amounts{total.settled, total.paidOut, toCollect} {
		earned.addAll(m)
	}

	var b strings.Builder
	b.WriteString("🤝 <b>COMISIÓN DEL VENDEDOR</b>\n")
	if from.Equal(until) {
		fmt.Fprintf(&b, "📅 %s %s", shortDays[from.Weekday()], shortDay(from))
	} else {
		fmt.Fprintf(&b, "📅 Del %s al %s", shortDay(from), shortDay(until))
	}
	if earned.orderCount == 0 {
		b.WriteString("\n\nNo hay ventas en esas fechas.\n")
		if s := balanceText(c.balance); s != "" {
			b.WriteString("\n" + s)
		}
		return b.String()
	}
	fmt.Fprintf(&b, " · %s\n", earned.account())

	// 1. How much to pay today.
	b.WriteString("\n━━━━━━━━━━━━━━━\n")
	today := total.paidOut.amt + c.balance
	switch {
	case today > 0.5:
		fmt.Fprintf(&b, "💰 <b>PARA PAGARLE HOY: %s</b>\n", formatCOP(today))
	case today < -0.5:
		fmt.Fprintf(&b, "💰 <b>HOY NO LE DEBES: él te debe %s</b>\n", formatCOP(-today))
	default:
		b.WriteString("💰 <b>HOY NO LE DEBES NADA</b>\n")
	}
	if c.balance > 0.5 || c.balance < -0.5 {
		fmt.Fprintf(&b, "   ✅ Cobrado sin pagarle: %s\n", formatCOP(total.paidOut.amt))
		if c.balance > 0 {
			fmt.Fprintf(&b, "   ↩️ Más lo que le quedó debiendo: +%s\n", formatCOP(c.balance))
		} else {
			fmt.Fprintf(&b, "   📣 Menos lo que él te debe (publicidad o adelantos): −%s\n", formatCOP(-c.balance))
		}
	} else if total.paidOut.orderCount > 0 {
		fmt.Fprintf(&b, "   ✅ Es lo cobrado sin pagarle (%s)\n", total.paidOut.account())
	}
	b.WriteString("   <i>Cuenta las ventas de estas fechas; para más días: /deuda mes</i>\n")
	b.WriteString("━━━━━━━━━━━━━━━\n")

	// 2. Summary by state.
	b.WriteString("\n📊 <b>RESUMEN</b>\n")
	if total.settled.orderCount > 0 {
		fmt.Fprintf(&b, "💵 Ya le pagaste: <b>%s</b> · %s\n", formatCOP(total.settled.amt), terminals(total.settled.unitCount))
	}
	fmt.Fprintf(&b, "✅ Cobrado, por pagarle: <b>%s</b> · %s\n", formatCOP(total.paidOut.amt), terminals(total.paidOut.unitCount))
	if toCollect.orderCount > 0 {
		fmt.Fprintf(&b, "⏳ Falta que el cliente pague: <b>%s</b> · %s\n", formatCOP(toCollect.amt), terminals(toCollect.unitCount))
		for _, x := range []struct {
			txt string
			m   amounts
		}{{"🚚 En camino", total.inTransit}, {"🏢 En oficina (el cliente debe reclamarlo)", total.office},
			{"⚠️ Con novedad o en devolución", total.issue}} {
			if x.m.orderCount > 0 {
				fmt.Fprintf(&b, "      %s: %s · %s\n", x.txt, formatCOP(x.m.amt), terminals(x.m.unitCount))
			}
		}
	}
	if total.failedOnes > 0 {
		fmt.Fprintf(&b, "❌ No se cobraron: %s (no suman comisión)\n", orderCount(total.failedOnes))
	}
	fmt.Fprintf(&b, "🏆 <b>Total ganado: %s</b> · %s\n", formatCOP(earned.amt), terminals(earned.unitCount))
	var ads, sellerAds float64
	for _, a := range c.ads {
		if a.Date >= from.Format("2006-01-02") && a.Date <= until.Format("2006-01-02") {
			ads += a.Amount
			sellerAds += a.VendorShare()
		}
	}
	if ads > 0 {
		fmt.Fprintf(&b, "📣 Publicidad en estas fechas: %s", formatCOP(ads))
		switch {
		case sellerAds < -0.5:
			fmt.Fprintf(&b, " (él pone %s)\n", formatCOP(-sellerAds))
		case sellerAds > 0.5:
			fmt.Fprintf(&b, " (le debes %s por lo que pagó de más)\n", formatCOP(sellerAds))
		default:
			b.WriteString(" (ya pagada mitad y mitad)\n")
		}
	}

	// 3. Day by day.
	b.WriteString("\n📅 <b>DÍA POR DÍA</b>\n")
	for t := until; !t.Before(from); t = t.AddDate(0, 0, -1) {
		d := days[t.Format("2006-01-02")]
		if d == nil {
			continue
		}
		var everything amounts
		for _, m := range []amounts{d.settled, d.paidOut, d.inTransit, d.office, d.issue} {
			everything.addAll(m)
		}
		if everything.orderCount+d.failedOnes == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n<b>%s %s</b>", shortDays[t.Weekday()], shortDay(t))
		if everything.orderCount > 0 {
			fmt.Fprintf(&b, " · %s · %s", terminals(everything.unitCount), formatCOP(everything.amt))
		}
		var chunks []string
		for _, x := range []struct {
			stage string
			m     amounts
		}{{"liquidado", d.settled}, {"pagado", d.paidOut}, {"camino", d.inTransit}, {"oficina", d.office}, {"novedad", d.issue}} {
			if x.m.orderCount > 0 {
				chunks = append(chunks, fmt.Sprintf("%s %s", icons[x.stage], formatCOP(x.m.amt)))
			}
		}
		if d.failedOnes > 0 {
			chunks = append(chunks, fmt.Sprintf("❌ %d sin cobrar", d.failedOnes))
		}
		b.WriteString("\n   " + strings.Join(chunks, " · ") + "\n")
		for _, v := range d.several {
			b.WriteString("   " + v + "\n")
		}
	}

	b.WriteString("\n<i>💵 pagado · ✅ cobrado, por pagarle · 🚚 en camino · 🏢 en oficina · ⚠️ novedad. ")
	b.WriteString("Lo que falta por cobrar se le paga cuando el cliente pague.</i>\n")
	if total.paidOut.orderCount > 0 {
		b.WriteString("<i>Cuando le pagues, regístralo con /pago (ej. <code>/pago 150000</code>).</i>\n")
	}
	if f.Commission.Estimated > 0 {
		fmt.Fprintf(&b, "\n⚠️ <i>No se pudo saber la cantidad de %d pedido(s) y se contaron como 1 datáfono: si alguno era de más, la deuda es mayor. Vuelve a pedir /deuda en un rato.</i>\n", f.Commission.Estimated)
	}
	if f.Truncated {
		b.WriteString("\n<i>Hay muchos envíos: los más antiguos pueden no estar incluidos.</i>\n")
	}
	return b.String()
}

func orderCount(n int) string {
	if n == 1 {
		return "1 pedido"
	}
	return fmt.Sprintf("%d pedidos", n)
}

func terminals(n int) string {
	if n == 1 {
		return "1 datáfono"
	}
	return fmt.Sprintf("%d datáfonos", n)
}
