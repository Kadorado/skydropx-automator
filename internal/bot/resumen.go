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

// Horas (de Colombia) de los resúmenes automáticos en el grupo de gestión.
const (
	horaResumenDiario  = 20 // cada noche: lo de hoy y lo que va del mes
	horaResumenSemanal = 8  // los lunes: la semana anterior
)

// watchResumenes manda el resumen diario y el semanal. Revisa cada pocos minutos y recuerda el último
// enviado, así un reinicio no lo repite y, si el servidor estaba apagado a la hora, lo manda al volver.
func (b *Bot) watchResumenes(ctx context.Context) {
	sleep(ctx, 2*time.Minute)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		now := time.Now().In(colombiaTZ)
		day := now.Format("2006-01-02")
		if now.Weekday() == time.Monday && now.Hour() >= horaResumenSemanal {
			if ok, _ := b.st.resumenPendiente(true, day); ok {
				b.enviarResumen(ctx, true)
			}
		}
		b.backupIfDue(ctx, now)
		if now.Hour() >= horaResumenDiario {
			if ok, _ := b.st.resumenPendiente(false, day); ok {
				b.enviarResumen(ctx, false)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) enviarResumen(ctx context.Context, semanal bool) {
	msg, err := b.resumen(ctx, semanal, time.Now().In(colombiaTZ))
	if err != nil {
		if !errors.Is(err, skydropx.ErrMockMode) {
			log.Printf("bot: no se pudo armar el resumen: %v", err)
		}
		return
	}
	for _, id := range b.gestionChats() {
		b.say(ctx, id, msg)
	}
}

func (b *Bot) comisionRate() skydropx.CommissionRate {
	if b.opts.Settings == nil {
		return skydropx.FlatCommission(0)
	}
	return b.opts.Settings.CommissionAt
}

// resumen arma el resumen del día (con lo que va del mes) o de la semana anterior (lunes a domingo).
func (b *Bot) resumen(ctx context.Context, semanal bool, now time.Time) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	hoy := now.Format("2006-01-02")
	if semanal {
		// Lunes a domingo de la semana anterior.
		offset := (int(now.Weekday()) + 6) % 7 // días desde el lunes
		lunes := now.AddDate(0, 0, -offset-7)
		domingo := lunes.AddDate(0, 0, 6)
		p, err := skydropx.NewPeriod(lunes.Format("2006-01-02"), domingo.Format("2006-01-02"))
		if err != nil {
			return "", err
		}
		f, err := b.sky.Finance(ctx, p, b.comisionRate())
		if err != nil {
			return "", err
		}
		titulo := fmt.Sprintf("📊 <b>Resumen de la semana</b> (%s al %s)", diaCorto(lunes), diaCorto(domingo))
		return resumenTexto(titulo, f, f.PerShipment, "Envíos de la semana"), nil
	}

	p, err := skydropx.NewPeriod(now.Format("2006-01")+"-01", hoy)
	if err != nil {
		return "", err
	}
	f, err := b.sky.Finance(ctx, p, b.comisionRate())
	if err != nil {
		return "", err
	}
	var deHoy []skydropx.ShipmentMoney
	for _, r := range f.PerShipment {
		if t, err := time.Parse(time.RFC3339, r.Date); err == nil && t.In(colombiaTZ).Format("2006-01-02") == hoy {
			deHoy = append(deHoy, r)
		}
	}
	titulo := fmt.Sprintf("📊 <b>Resumen del %s</b>", diaCorto(now))
	return resumenTexto(titulo, f, deHoy, "Hoy"), nil
}

// resumenTexto arma el mensaje: las guías del bloque destacado (hoy o la semana) y el dinero del periodo.
func resumenTexto(titulo string, f skydropx.Finance, guias []skydropx.ShipmentMoney, etiqueta string) string {
	var b strings.Builder
	b.WriteString(titulo + "\n\n")
	n, gasto := gastoEnvios(guias)
	fmt.Fprintf(&b, "<b>%s</b>\n📦 Guías generadas: %d · pagaste %s\n\n", etiqueta, n, formatCOP(gasto))
	if etiqueta == "Hoy" {
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

// gastoEnvios suma lo que realmente salió de los créditos por las guías (guía + tarifa + protección
// + cargos extra, menos devoluciones), igual que Finanzas. Las canceladas no cuentan.
func gastoEnvios(rows []skydropx.ShipmentMoney) (int, float64) {
	n, total := 0, 0.0
	for _, r := range rows {
		if r.Status == "cancelled" {
			continue
		}
		costo := r.LabelCost
		if r.Charged > 0 {
			costo = r.Charged
		}
		n++
		total += costo + r.Extra - r.Returned
	}
	return n, total
}

var mesesCortos = [...]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}

func diaCorto(t time.Time) string {
	return fmt.Sprintf("%d %s", t.Day(), mesesCortos[t.Month()-1])
}

// comandoComision atiende /comision: sin valor muestra la comisión; con valor la cambia desde hoy
// o desde la fecha indicada (/comision 12000 2026-11-01).
func (b *Bot) comandoComision(ctx context.Context, chatID int64, text string) {
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
	valor, err := strconv.ParseFloat(strings.NewReplacer(".", "", "$", "", ",", "").Replace(args[0]), 64)
	if err != nil {
		b.say(ctx, chatID, "No entendí el valor. Ejemplo: <code>/comision 12000</code>")
		return
	}
	desde := ""
	if len(args) > 1 {
		desde = args[1]
	}
	r, err := b.opts.Settings.SetCommission(valor, desde)
	if err != nil {
		b.say(ctx, chatID, "No se pudo cambiar: "+err.Error())
		return
	}
	b.say(ctx, chatID, fmt.Sprintf("✅ Comisión: <b>%s</b> por datáfono desde el %s. Las ventas anteriores conservan su valor.", formatCOP(r.Value), r.From))
}

// comandoResumen atiende /resumen (hoy y el mes) y /resumen semana (la semana anterior).
func (b *Bot) comandoResumen(ctx context.Context, chatID int64, text string) {
	semanal := strings.Contains(strings.ToLower(text), "semana")
	b.say(ctx, chatID, "⏳ Armando el resumen…")
	msg, err := b.resumen(ctx, semanal, time.Now().In(colombiaTZ))
	if err != nil {
		b.say(ctx, chatID, "No se pudo armar el resumen: "+err.Error())
		return
	}
	b.say(ctx, chatID, msg)
}
