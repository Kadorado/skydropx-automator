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
	f, err := b.sky.FinanceCompleta(ctx, p, b.comisionRate())
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
	var c cuentaVendedor
	if b.opts.Settings != nil {
		c = cuentaVendedor{b.opts.Settings.PaidTrackings(), b.opts.Settings.AdExpenses(), b.opts.Settings.VendorBalance()}
	}
	b.say(ctx, chatID, deudaTexto(desde, hasta, f, estados, c))
}

const ayudaDeuda = `Ejemplos:
<code>/deuda</code> → últimos 7 días
<code>/deuda 3</code> → últimos 3 días
<code>/deuda hoy</code> · <code>/deuda ayer</code>
<code>/deuda semana</code> → la semana anterior (lunes a domingo)
<code>/deuda mes</code> → lo que va del mes
<code>/deuda 2026-11-03</code> → ese día
<code>/deuda 2026-11-01 2026-11-10</code> → ese rango`

// monto son los pedidos, sus datáfonos y la comisión que suman.
type monto struct {
	pedidos, unidades int
	valor             float64
}

func (m *monto) sumar(r skydropx.ShipmentMoney) {
	m.pedidos++
	m.unidades += r.Units
	m.valor += r.Commission
}

func (m *monto) juntar(o monto) {
	m.pedidos += o.pedidos
	m.unidades += o.unidades
	m.valor += o.valor
}

// cuenta dice cuántos pedidos y datáfonos son, sin repetir cuando cada pedido es de un datáfono.
func (m monto) cuenta() string {
	if m.pedidos == m.unidades {
		return datafonos(m.unidades)
	}
	return pedidos(m.pedidos) + " = " + datafonos(m.unidades)
}

// cuentaVendedor es lo registrado a mano con el vendedor: pagos, publicidad compartida y el saldo de ambos.
type cuentaVendedor struct {
	pagadas map[string]string // guía -> día en que se le pagó
	ads     []settings.AdExpense
	saldo   float64
}

// deudaTexto arma el mensaje de /deuda: cada día con lo que ya se le pagó al vendedor (guías pagadas),
// lo que se le debe (envíos cobrados) y lo que se le deberá (envíos por cobrar, separados en camino, en
// oficina o con novedad según estados), los pedidos de varios datáfonos uno por uno, el total, la
// publicidad de esos días y el saldo de pagos y publicidad. Las ventas cuentan el día en que se generó la guía.
func deudaTexto(desde, hasta time.Time, f skydropx.Finance, estados map[string]string, c cuentaVendedor) string {
	pagadas := c.pagadas
	type dia struct {
		liquidado, pagado, camino, oficina, novedad monto
		fallidos                                    int
	}
	iconos := map[string]string{"liquidado": "💵", "pagado": "✅", "camino": "🚚", "oficina": "🏢", "novedad": "⚠️"}
	dias := map[string]*dia{}
	var total dia
	var varios []string // pedidos de más de un datáfono, del más nuevo al más antiguo
	for _, r := range f.PerShipment {
		t, err := time.Parse(time.RFC3339, r.Date)
		if err != nil {
			continue
		}
		t = t.In(colombiaTZ)
		d := dias[t.Format("2006-01-02")]
		if d == nil {
			d = &dia{}
			dias[t.Format("2006-01-02")] = d
		}
		estado := ""
		switch r.Status {
		case "collected":
			if pagadas[r.Tracking] != "" {
				estado = "liquidado"
				d.liquidado.sumar(r)
				total.liquidado.sumar(r)
				break
			}
			estado = "pagado"
			d.pagado.sumar(r)
			total.pagado.sumar(r)
		case "pending":
			switch estados[r.Tracking] {
			case "oficina":
				estado = "oficina"
				d.oficina.sumar(r)
				total.oficina.sumar(r)
			case "novedad":
				estado = "novedad"
				d.novedad.sumar(r)
				total.novedad.sumar(r)
			default:
				estado = "camino"
				d.camino.sumar(r)
				total.camino.sumar(r)
			}
		case "failed":
			d.fallidos++
		}
		if estado != "" && r.Units > 1 {
			cliente := strings.TrimSpace(r.Customer)
			if cliente == "" {
				cliente = "guía " + r.Tracking
			}
			varios = append(varios, fmt.Sprintf("• %s %s · %s: <b>%s</b> × %s = %s %s",
				diasCortos[t.Weekday()], diaCorto(t), html.EscapeString(cliente), datafonos(r.Units),
				formatCOP(r.Commission/float64(r.Units)), formatCOP(r.Commission), iconos[estado]))
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
		if d == nil {
			continue
		}
		var todo monto
		for _, m := range []monto{d.liquidado, d.pagado, d.camino, d.oficina, d.novedad} {
			todo.juntar(m)
		}
		if todo.pedidos+d.fallidos == 0 {
			continue
		}
		if !hayVentas {
			b.WriteString("<b>Por día</b> (💵 ya le pagaste · ✅ cobrado, por pagarle · 🚚 en camino · 🏢 en oficina · ⚠️ con novedad)\n")
			hayVentas = true
		}
		fmt.Fprintf(&b, "• <b>%s %s</b>", diasCortos[t.Weekday()], diaCorto(t))
		if todo.pedidos > 0 {
			fmt.Fprintf(&b, " · %s", todo.cuenta())
		}
		var partes []string
		for _, x := range []struct {
			estado string
			m      monto
		}{{"liquidado", d.liquidado}, {"pagado", d.pagado}, {"camino", d.camino}, {"oficina", d.oficina}, {"novedad", d.novedad}} {
			if x.m.pedidos > 0 {
				partes = append(partes, fmt.Sprintf("%s %s (%s)", iconos[x.estado], formatCOP(x.m.valor), datafonos(x.m.unidades)))
			}
		}
		if d.fallidos > 0 {
			partes = append(partes, fmt.Sprintf("❌ %d sin cobrar", d.fallidos))
		}
		b.WriteString("\n   " + strings.Join(partes, " · ") + "\n")
	}
	if !hayVentas {
		b.WriteString("No hay ventas en esas fechas.\n")
		return b.String()
	}

	if len(varios) > 0 {
		b.WriteString("\n📦 <b>Pedidos de más de un datáfono</b> (cada datáfono suma su comisión)\n")
		b.WriteString(strings.Join(varios, "\n") + "\n")
	}

	var acumulado monto
	for _, m := range []monto{total.liquidado, total.pagado, total.camino, total.oficina, total.novedad} {
		acumulado.juntar(m)
	}
	fmt.Fprintf(&b, "\n🏆 <b>Acumulado en esos días: %s</b> (%s)\n", formatCOP(acumulado.valor), acumulado.cuenta())
	if total.liquidado.pedidos > 0 {
		fmt.Fprintf(&b, "💵 Ya le pagaste: %s (%s)\n", formatCOP(total.liquidado.valor), total.liquidado.cuenta())
	}
	fmt.Fprintf(&b, "✅ <b>Se le debe: %s</b> (%s, ya cobrados)\n", formatCOP(total.pagado.valor), total.pagado.cuenta())
	if total.camino.pedidos > 0 {
		fmt.Fprintf(&b, "🚚 En camino: %s (%s)\n", formatCOP(total.camino.valor), total.camino.cuenta())
	}
	if total.oficina.pedidos > 0 {
		fmt.Fprintf(&b, "🏢 En oficina: %s (%s); el cliente debe reclamarlo\n", formatCOP(total.oficina.valor), total.oficina.cuenta())
	}
	if total.novedad.pedidos > 0 {
		fmt.Fprintf(&b, "⚠️ Con novedad: %s (%s); intento fallido, novedad o en devolución\n", formatCOP(total.novedad.valor), total.novedad.cuenta())
	}
	b.WriteString("<i>Lo que no está cobrado se le debe cuando el cliente pague.</i>\n")
	var ads, adsVendedor float64
	for _, a := range c.ads {
		if a.Date >= desde.Format("2006-01-02") && a.Date <= hasta.Format("2006-01-02") {
			ads += a.Amount
			adsVendedor += a.VendorShare()
		}
	}
	if ads > 0 {
		fmt.Fprintf(&b, "\n📣 Publicidad en esos días: %s; ", formatCOP(ads))
		switch {
		case adsVendedor < -0.5:
			fmt.Fprintf(&b, "él pone %s de su comisión\n", formatCOP(-adsVendedor))
		case adsVendedor > 0.5:
			fmt.Fprintf(&b, "le debes %s por lo que pagó de más\n", formatCOP(adsVendedor))
		default:
			b.WriteString("ya pagaron mitad y mitad\n")
		}
	}
	if s := saldoTexto(c.saldo); s != "" {
		b.WriteString("\n" + s)
	}
	if total.pagado.pedidos > 0 {
		b.WriteString("<i>Cuando le pagues, regístralo con /pago (ej. <code>/pago 150000</code>).</i>\n")
	}
	if c := f.Commission; c.Estimated > 0 {
		fmt.Fprintf(&b, "\n⚠️ <i>No se pudo saber la cantidad de %d pedido(s) y se contaron como 1 datáfono: si alguno era de más, la deuda es mayor. Vuelve a pedir /deuda en un rato.</i>\n", c.Estimated)
	}
	if f.Truncated {
		b.WriteString("\n<i>Hay muchos envíos: los más antiguos pueden no estar incluidos.</i>\n")
	}
	return b.String()
}

func pedidos(n int) string {
	if n == 1 {
		return "1 pedido"
	}
	return fmt.Sprintf("%d pedidos", n)
}

func datafonos(n int) string {
	if n == 1 {
		return "1 datáfono"
	}
	return fmt.Sprintf("%d datáfonos", n)
}
