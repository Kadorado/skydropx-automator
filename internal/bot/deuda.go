package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"skydropx-automator/internal/skydropx"
)

// maxDiasDeuda limita el rango de /deuda para que el mensaje quepa en Telegram.
const maxDiasDeuda = 62

var diasCortos = [...]string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}

// rangoDeuda entiende el rango de /deuda (fechas en hora de Colombia, ambas incluidas):
// vacío = últimos 7 días, "5" = últimos 5 días, "hoy", "ayer", "semana" = la anterior (lunes a domingo),
// "mes" = el mes en curso, "2026-11-03" = ese día, "2026-11-01 2026-11-10" = ese rango.
func rangoDeuda(args string, now time.Time) (desde, hasta time.Time, err error) {
	now = now.In(colombiaTZ)
	hoy := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, colombiaTZ)
	f := strings.Fields(strings.ToLower(args))
	switch {
	case len(f) == 0:
		return hoy.AddDate(0, 0, -6), hoy, nil
	case len(f) == 1 && f[0] == "hoy":
		return hoy, hoy, nil
	case len(f) == 1 && f[0] == "ayer":
		return hoy.AddDate(0, 0, -1), hoy.AddDate(0, 0, -1), nil
	case len(f) == 1 && f[0] == "semana":
		offset := (int(hoy.Weekday()) + 6) % 7 // días desde el lunes
		lunes := hoy.AddDate(0, 0, -offset-7)
		return lunes, lunes.AddDate(0, 0, 6), nil
	case len(f) == 1 && f[0] == "mes":
		return hoy.AddDate(0, 0, 1-hoy.Day()), hoy, nil
	case len(f) == 1:
		if n, err := strconv.Atoi(f[0]); err == nil {
			if n < 1 || n > maxDiasDeuda {
				return desde, hasta, fmt.Errorf("los días van de 1 a %d", maxDiasDeuda)
			}
			return hoy.AddDate(0, 0, 1-n), hoy, nil
		}
		d, err := time.ParseInLocation("2006-01-02", f[0], colombiaTZ)
		if err != nil {
			return desde, hasta, fmt.Errorf("no entendí %q", f[0])
		}
		return d, d, nil
	case len(f) == 2:
		d, err1 := time.ParseInLocation("2006-01-02", f[0], colombiaTZ)
		h, err2 := time.ParseInLocation("2006-01-02", f[1], colombiaTZ)
		if err1 != nil || err2 != nil {
			return desde, hasta, errors.New("las fechas van como AAAA-MM-DD")
		}
		if d.After(h) {
			d, h = h, d
		}
		if h.Sub(d) >= maxDiasDeuda*24*time.Hour {
			return desde, hasta, fmt.Errorf("el rango puede ser de hasta %d días", maxDiasDeuda)
		}
		return d, h, nil
	}
	return desde, hasta, errors.New("demasiados datos")
}

// comandoDeuda atiende /deuda: lo que se le debe al vendedor en un rango de días, día por día.
func (b *Bot) comandoDeuda(ctx context.Context, chatID int64, text string) {
	desde, hasta, err := rangoDeuda(strings.TrimPrefix(text, "/deuda"), time.Now())
	if err != nil {
		b.say(ctx, chatID, "No entendí el rango: "+err.Error()+".\n\n"+ayudaDeuda)
		return
	}
	b.say(ctx, chatID, "⏳ Sumando la comisión y revisando el rastreo de lo que falta por cobrar…")
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	p, err := skydropx.NewPeriod(desde.Format("2006-01-02"), hasta.Format("2006-01-02"))
	if err != nil {
		b.say(ctx, chatID, "No se pudo armar el resumen: "+err.Error())
		return
	}
	f, err := b.sky.Finance(ctx, p, b.comisionRate())
	if err != nil {
		b.say(ctx, chatID, "No se pudo armar el resumen: "+err.Error())
		return
	}
	estados := map[string]string{} // guía -> camino | oficina | novedad
	for _, r := range f.PerShipment {
		if r.Status == "pending" {
			estados[r.Tracking] = b.sky.EstadoPorCobrar(ctx, r.Tracking, r.Carrier)
		}
	}
	b.say(ctx, chatID, deudaTexto(desde, hasta, f, estados))
}

const ayudaDeuda = `Ejemplos:
<code>/deuda</code> → últimos 7 días
<code>/deuda 3</code> → últimos 3 días
<code>/deuda hoy</code> · <code>/deuda ayer</code>
<code>/deuda semana</code> → la semana anterior (lunes a domingo)
<code>/deuda mes</code> → lo que va del mes
<code>/deuda 2026-11-03</code> → ese día
<code>/deuda 2026-11-01 2026-11-10</code> → ese rango`

// monto es una cantidad de datáfonos y la comisión que suman.
type monto struct {
	unidades int
	valor    float64
}

func (m *monto) sumar(r skydropx.ShipmentMoney) {
	m.unidades += r.Units
	m.valor += r.Commission
}

// deudaTexto arma el mensaje de /deuda: cada día con lo que ya se le debe (envíos cobrados) y lo que
// se le deberá (envíos por cobrar, separados en camino, en oficina o con novedad según estados), y el total.
// Las ventas cuentan el día en que se generó la guía.
func deudaTexto(desde, hasta time.Time, f skydropx.Finance, estados map[string]string) string {
	type dia struct {
		pagado, camino, oficina, novedad monto
		fallidos                         int
	}
	dias := map[string]*dia{}
	var total dia
	for _, r := range f.PerShipment {
		t, err := time.Parse(time.RFC3339, r.Date)
		if err != nil {
			continue
		}
		key := t.In(colombiaTZ).Format("2006-01-02")
		d := dias[key]
		if d == nil {
			d = &dia{}
			dias[key] = d
		}
		switch r.Status {
		case "collected":
			d.pagado.sumar(r)
			total.pagado.sumar(r)
		case "pending":
			switch estados[r.Tracking] {
			case "oficina":
				d.oficina.sumar(r)
				total.oficina.sumar(r)
			case "novedad":
				d.novedad.sumar(r)
				total.novedad.sumar(r)
			default:
				d.camino.sumar(r)
				total.camino.sumar(r)
			}
		case "failed":
			d.fallidos++
		}
	}

	var b strings.Builder
	b.WriteString("🤝 <b>Comisión del vendedor</b>\n")
	if desde.Equal(hasta) {
		fmt.Fprintf(&b, "%s %s\n\n", diasCortos[desde.Weekday()], diaCorto(desde))
	} else {
		fmt.Fprintf(&b, "Del %s al %s\n\n", diaCorto(desde), diaCorto(hasta))
	}

	hayVentas := false
	for t := hasta; !t.Before(desde); t = t.AddDate(0, 0, -1) {
		d := dias[t.Format("2006-01-02")]
		if d == nil || d.pagado.unidades+d.camino.unidades+d.oficina.unidades+d.novedad.unidades+d.fallidos == 0 {
			continue
		}
		if !hayVentas {
			b.WriteString("<b>Por día</b> (✅ pagado · 🚚 en camino · 🏢 en oficina · ⚠️ con novedad)\n")
			hayVentas = true
		}
		fmt.Fprintf(&b, "• <b>%s %s</b>:", diasCortos[t.Weekday()], diaCorto(t))
		var partes []string
		for _, x := range []struct {
			icono string
			m     monto
		}{{"✅", d.pagado}, {"🚚", d.camino}, {"🏢", d.oficina}, {"⚠️", d.novedad}} {
			if x.m.unidades > 0 {
				partes = append(partes, fmt.Sprintf("%s %s (%d)", x.icono, formatCOP(x.m.valor), x.m.unidades))
			}
		}
		if d.fallidos > 0 {
			partes = append(partes, fmt.Sprintf("❌ %d sin cobrar", d.fallidos))
		}
		b.WriteString(" " + strings.Join(partes, " · ") + "\n")
	}
	if !hayVentas {
		b.WriteString("No hay ventas en esas fechas.\n")
		return b.String()
	}

	acumulado := total.pagado.valor + total.camino.valor + total.oficina.valor + total.novedad.valor
	unidades := total.pagado.unidades + total.camino.unidades + total.oficina.unidades + total.novedad.unidades
	fmt.Fprintf(&b, "\n🏆 <b>Acumulado en esos días: %s</b> (%s)\n", formatCOP(acumulado), datafonos(unidades))
	fmt.Fprintf(&b, "✅ <b>Se le debe: %s</b> (%s ya pagados)\n", formatCOP(total.pagado.valor), datafonos(total.pagado.unidades))
	if total.camino.unidades > 0 {
		fmt.Fprintf(&b, "🚚 En camino: %s (%s)\n", formatCOP(total.camino.valor), datafonos(total.camino.unidades))
	}
	if total.oficina.unidades > 0 {
		fmt.Fprintf(&b, "🏢 En oficina: %s (%s); el cliente debe reclamarlo\n", formatCOP(total.oficina.valor), datafonos(total.oficina.unidades))
	}
	if total.novedad.unidades > 0 {
		fmt.Fprintf(&b, "⚠️ Con novedad: %s (%s); intento fallido, novedad o en devolución\n", formatCOP(total.novedad.valor), datafonos(total.novedad.unidades))
	}
	b.WriteString("<i>Lo que no está pagado se le debe cuando el cliente pague.</i>\n")
	if c := f.Commission; c.Estimated > 0 {
		fmt.Fprintf(&b, "\n<i>%d envío(s) sin cantidad conocida se contaron como 1 datáfono.</i>\n", c.Estimated)
	}
	if f.Truncated {
		b.WriteString("\n<i>Hay muchos envíos: los más antiguos pueden no estar incluidos.</i>\n")
	}
	return b.String()
}

func datafonos(n int) string {
	if n == 1 {
		return "1 datáfono"
	}
	return fmt.Sprintf("%d datáfonos", n)
}
