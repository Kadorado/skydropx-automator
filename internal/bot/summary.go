package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"skydropx-automator/internal/skydropx"
)

// Hours (Colombia time) of the automatic summaries in the ops group.
const (
	dailySummaryHour  = 20          // every night: today's and the month so far
	weeklySummaryHour = morningHour // Mondays: the previous week
)

// watchSummaries sends the daily and weekly summaries. It checks every few minutes and remembers the last one
// sent, so a restart doesn't repeat it and, if the server was down at that hour, it sends it on return.
func (b *Bot) watchSummaries(ctx context.Context) {
	sleep(ctx, 2*time.Minute)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		now := time.Now().In(colombiaTZ)
		day := now.Format("2006-01-02")
		// Morning, always in this order: backup and weekly summary (Mondays), and last what is left
		// to dispatch (workdays), which is what needs doing and stays last in the chat.
		b.backupIfDue(ctx, now)
		if now.Weekday() == time.Monday && now.Hour() >= weeklySummaryHour {
			if ok, _ := b.st.summaryDue(true, day); ok {
				b.sendSummary(ctx, true)
			}
		}
		b.dispatchReminderIfDue(ctx, now)
		// Night: the day's summary.
		if now.Hour() >= dailySummaryHour {
			if ok, _ := b.st.summaryDue(false, day); ok {
				b.sendSummary(ctx, false)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) sendSummary(ctx context.Context, weekly bool) {
	msg, err := b.summary(ctx, weekly, time.Now().In(colombiaTZ))
	if err != nil {
		if !errors.Is(err, skydropx.ErrMockMode) {
			log.Printf("bot: no se pudo armar el resumen: %v", err)
		}
		return
	}
	for _, id := range b.opsChats() {
		b.say(ctx, id, msg)
	}
}

func (b *Bot) commissionRate() skydropx.CommissionRate {
	if b.opts.Settings == nil {
		return skydropx.FlatCommission(0)
	}
	return b.opts.Settings.CommissionAt
}

// summary builds the day's summary (with the month so far) or the previous week's (Monday to Sunday).
func (b *Bot) summary(ctx context.Context, weekly bool, now time.Time) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	today := now.Format("2006-01-02")
	if weekly {
		// Monday to Sunday of the previous week.
		offset := (int(now.Weekday()) + 6) % 7 // days since Monday
		weekStart := now.AddDate(0, 0, -offset-7)
		sunday := weekStart.AddDate(0, 0, 6)
		p, err := skydropx.NewPeriod(weekStart.Format("2006-01-02"), sunday.Format("2006-01-02"))
		if err != nil {
			return "", err
		}
		f, err := b.sky.Finance(ctx, p, b.commissionRate())
		if err != nil {
			return "", err
		}
		title := fmt.Sprintf("📊 <b>Resumen de la semana</b> (%s al %s)", shortDay(weekStart), shortDay(sunday))
		return summaryText(title, f, f.PerShipment, "Envíos de la semana"), nil
	}

	p, err := skydropx.NewPeriod(now.Format("2006-01")+"-01", today)
	if err != nil {
		return "", err
	}
	f, err := b.sky.Finance(ctx, p, b.commissionRate())
	if err != nil {
		return "", err
	}
	var todays []skydropx.ShipmentMoney
	for _, r := range f.PerShipment {
		if t, err := time.Parse(time.RFC3339, r.Date); err == nil && t.In(colombiaTZ).Format("2006-01-02") == today {
			todays = append(todays, r)
		}
	}
	title := fmt.Sprintf("📊 <b>Resumen del %s</b>", shortDay(now))
	return summaryText(title, f, todays, "Hoy"), nil
}

// summaryText builds the message: the labels of the highlighted block (today or the week) and the period's money.
func summaryText(title string, f skydropx.Finance, lbls []skydropx.ShipmentMoney, labelImg string) string {
	var b strings.Builder
	b.WriteString(title + "\n\n")
	n, expense := shippingExpense(lbls)
	fmt.Fprintf(&b, "<b>%s</b>\n📦 Guías generadas: %d · pagaste %s\n\n", labelImg, n, formatCOP(expense))
	if labelImg == "Hoy" {
		b.WriteString("<b>En lo que va del mes</b>\n")
	} else {
		b.WriteString("<b>Cobro de esos envíos</b>\n")
	}
	fmt.Fprintf(&b, "💵 Cobrado: %d · %s\n", f.Collected.Count, formatCOP(f.Collected.Amount))
	fmt.Fprintf(&b, "⏳ Por cobrar: %d · %s\n", f.Pending.Count, formatCOP(f.Pending.Amount))
	fmt.Fprintf(&b, "❌ No cobrado: %d · %s\n", f.Failed.Count, formatCOP(f.Failed.Amount))
	c := f.Commission
	fmt.Fprintf(&b, "🤝 Comisión del vendedor: %s de lo cobrado · %s pendiente\n", formatCOP(c.PaidAmount), formatCOP(c.PendingAmount))
	if f.Balance != nil {
		fmt.Fprintf(&b, "💳 Saldo en créditos: %s\n", formatCOP(*f.Balance))
	}
	return b.String()
}

// shippingExpense adds up what actually came out of the credits for the labels (label + fee + protection
// + extra charges, minus refunds), same as Finanzas. Cancelled ones don't count.
func shippingExpense(rows []skydropx.ShipmentMoney) (int, float64) {
	n, total := 0, 0.0
	for _, r := range rows {
		if r.Status == "cancelled" {
			continue
		}
		cost := r.LabelCost
		if r.Charged > 0 {
			cost = r.Charged
		}
		n++
		total += cost + r.Extra - r.Returned
	}
	return n, total
}

var shortMonths = [...]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}

func shortDay(t time.Time) string {
	return fmt.Sprintf("%d %s", t.Day(), shortMonths[t.Month()-1])
}

// commissionCommand handles /comision: without a value it shows the commission; with a value it changes it from today
// or from the given date (/comision 12000 2026-11-01).
func (b *Bot) commissionCommand(ctx context.Context, chatID int64, text string) {
	if b.opts.Settings == nil {
		b.say(ctx, chatID, "La comisión no se puede cambiar desde aquí.")
		return
	}
	args := strings.Fields(strings.TrimPrefix(text, "/comision"))
	if len(args) == 0 {
		msg := fmt.Sprintf("🤝 Comisión actual: <b>%s</b> por datáfono.", formatCOP(b.opts.Settings.CurrentCommission()))
		if h := b.opts.Settings.CommissionHistory(); len(h) > 0 {
			msg += "\n\nCambios:"
			for _, r := range h {
				msg += fmt.Sprintf("\n• %s desde el %s", formatCOP(r.Value), r.From)
			}
		}
		b.say(ctx, chatID, msg+"\n\nPara cambiarla: <code>/comision 12000</code> (desde hoy) o <code>/comision 12000 2026-11-01</code>.")
		return
	}
	amt, err := strconv.ParseFloat(strings.NewReplacer(".", "", "$", "", ",", "").Replace(args[0]), 64)
	if err != nil {
		b.say(ctx, chatID, "No entendí el valor. Ejemplo: <code>/comision 12000</code>")
		return
	}
	from := ""
	if len(args) > 1 {
		from = args[1]
	}
	r, err := b.opts.Settings.SetCommission(amt, from)
	if err != nil {
		b.say(ctx, chatID, "No se pudo cambiar: "+err.Error())
		return
	}
	b.say(ctx, chatID, fmt.Sprintf("✅ Comisión: <b>%s</b> por datáfono desde el %s. Las ventas anteriores conservan su valor.", formatCOP(r.Value), r.From))
}

// summaryCommand handles /resumen (today and the month) and /resumen semana (the previous week).
func (b *Bot) summaryCommand(ctx context.Context, chatID int64, text string) {
	weekly := strings.Contains(strings.ToLower(text), "semana")
	b.say(ctx, chatID, "⏳ Armando el resumen…")
	msg, err := b.summary(ctx, weekly, time.Now().In(colombiaTZ))
	if err != nil {
		b.say(ctx, chatID, "No se pudo armar el resumen: "+err.Error())
		return
	}
	b.say(ctx, chatID, msg)
}
