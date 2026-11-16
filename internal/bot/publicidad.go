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
)

const ayudaPublicidad = `La publicidad se reparte mitad y mitad con el vendedor:
<code>/publicidad 40000</code> → la pagaste tú: él te debe la mitad y se descuenta de su comisión
<code>/publicidad 40000 vendedor</code> → la pagó él: se le suma la mitad a lo que se le debe
<code>/publicidad 40000 ayer facebook</code> → con fecha (hoy, ayer o AAAA-MM-DD) y una nota
<code>/publicidad</code> → ver lo registrado · <code>/publicidad borrar</code> → borrar el último`

// argsPublicidad entiende "/publicidad 40000 [vendedor|yo] [hoy|ayer|AAAA-MM-DD] [nota…]".
func argsPublicidad(args string, now time.Time) (settings.AdExpense, error) {
	hoy := now.In(colombiaTZ)
	a := settings.AdExpense{Date: hoy.Format("2006-01-02"), PaidBy: "yo"}
	f := strings.Fields(args)
	if len(f) == 0 {
		return a, errors.New("falta el valor")
	}
	v, err := strconv.ParseFloat(strings.NewReplacer(".", "", "$", "", ",", "").Replace(f[0]), 64)
	if err != nil || v <= 0 {
		return a, fmt.Errorf("no entendí el valor %q", f[0])
	}
	a.Amount = v
	var nota []string
	for i, w := range f[1:] {
		switch lw := strings.ToLower(w); {
		case i < 2 && (lw == "vendedor" || lw == "él" || lw == "el"):
			a.PaidBy = "vendedor"
		case i < 2 && lw == "yo":
			a.PaidBy = "yo"
		case i < 2 && lw == "hoy":
		case i < 2 && lw == "ayer":
			a.Date = hoy.AddDate(0, 0, -1).Format("2006-01-02")
		default:
			if _, err := time.ParseInLocation("2006-01-02", w, colombiaTZ); err == nil && i < 2 {
				a.Date = w
				continue
			}
			nota = append(nota, w)
		}
	}
	a.Note = strings.Join(nota, " ")
	return a, nil
}

// comandoPublicidad atiende /publicidad: registra, lista o borra gastos de publicidad compartidos.
func (b *Bot) comandoPublicidad(ctx context.Context, chatID int64, text string) {
	if b.opts.Settings == nil {
		b.say(ctx, chatID, "No hay archivo de ajustes configurado: no se puede guardar la publicidad.")
		return
	}
	args := strings.TrimSpace(strings.TrimPrefix(text, "/publicidad"))
	switch {
	case args == "":
		b.say(ctx, chatID, publicidadTexto(b.opts.Settings.AdExpenses(), b.opts.Settings.VendorBalance(), time.Now()))
		return
	case strings.EqualFold(args, "borrar"):
		a, ok, err := b.opts.Settings.RemoveLastAd()
		switch {
		case err != nil:
			b.say(ctx, chatID, "No se pudo borrar: "+err.Error())
		case !ok:
			b.say(ctx, chatID, "No hay publicidad registrada.")
		default:
			b.say(ctx, chatID, fmt.Sprintf("🗑️ Borré la publicidad de %s del %s.\n%s", formatCOP(a.Amount), a.Date,
				saldoTexto(b.opts.Settings.VendorBalance())))
		}
		return
	}
	a, err := argsPublicidad(args, time.Now())
	if err != nil {
		b.say(ctx, chatID, "No entendí: "+err.Error()+".\n\n"+ayudaPublicidad)
		return
	}
	if err := b.opts.Settings.AddAd(a); err != nil {
		b.say(ctx, chatID, "No se pudo guardar: "+err.Error())
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "📣 <b>Publicidad registrada: %s</b> (%s)", formatCOP(a.Amount), a.Date)
	if a.Note != "" {
		sb.WriteString(" · " + html.EscapeString(a.Note))
	}
	if a.PaidBy == "vendedor" {
		fmt.Fprintf(&sb, "\nLa pagó el vendedor: se le suma la mitad, <b>%s</b>.\n", formatCOP(a.Amount/2))
	} else {
		fmt.Fprintf(&sb, "\nLa pagaste tú: el vendedor pone la mitad, <b>%s</b>, que se descuenta de su comisión.\n", formatCOP(a.Amount/2))
	}
	sb.WriteString(saldoTexto(b.opts.Settings.VendorBalance()))
	sb.WriteString("<i>Si te equivocaste: /publicidad borrar</i>\n")
	b.say(ctx, chatID, sb.String())
}

// publicidadTexto lista los últimos gastos de publicidad, el total del mes y cómo queda la cuenta.
func publicidadTexto(ads []settings.AdExpense, saldo float64, now time.Time) string {
	if len(ads) == 0 {
		return "No hay publicidad registrada.\n\n" + ayudaPublicidad
	}
	mes := now.In(colombiaTZ).Format("2006-01")
	var totalMes, yoMes, vendedorMes float64
	for _, a := range ads {
		if strings.HasPrefix(a.Date, mes) {
			totalMes += a.Amount
			if a.PaidBy == "vendedor" {
				vendedorMes += a.Amount
			} else {
				yoMes += a.Amount
			}
		}
	}
	var sb strings.Builder
	sb.WriteString("📣 <b>Publicidad compartida</b> (mitad y mitad, la más reciente primero)\n")
	for i, n := len(ads)-1, 0; i >= 0 && n < 15; i, n = i-1, n+1 {
		a := ads[i]
		quien := "tú"
		if a.PaidBy == "vendedor" {
			quien = "vendedor"
		}
		fmt.Fprintf(&sb, "• %s: <b>%s</b> · pagó %s", a.Date, formatCOP(a.Amount), quien)
		if a.Note != "" {
			sb.WriteString(" · " + html.EscapeString(a.Note))
		}
		sb.WriteString("\n")
	}
	if totalMes > 0 {
		fmt.Fprintf(&sb, "\n🗓️ Este mes: %s (tú %s · vendedor %s); a cada uno le toca %s\n",
			formatCOP(totalMes), formatCOP(yoMes), formatCOP(vendedorMes), formatCOP(totalMes/2))
	}
	if s := saldoTexto(saldo); s != "" {
		sb.WriteString("\n" + s)
	} else {
		sb.WriteString("\n✅ Están a paz y salvo con pagos y publicidad.\n")
	}
	return sb.String()
}
