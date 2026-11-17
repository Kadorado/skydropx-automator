package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	"skydropx-automator/internal/skydropx"
	"skydropx-automator/internal/workdays"
)

const (
	// morningHour is when the morning messages go out, in this order: backup and weekly summary
	// (Mondays), then what is left to dispatch (workdays).
	morningHour = 8
	// dispatchWindow is how far back labels are checked for packages not taken to the carrier.
	dispatchWindow = 10 * 24 * time.Hour
)

func (s *State) dispatchReminderDue(day string) (bool, error) {
	s.mu.Lock()
	if s.LastDispatchReminder == day {
		s.mu.Unlock()
		return false, nil
	}
	s.LastDispatchReminder = day
	s.mu.Unlock()
	return true, s.save()
}

// dispatchReminderIfDue tells the gestión group, on workday mornings before the office closes, which
// labels still have to be taken to the carrier. Nothing pending, nothing said.
func (b *Bot) dispatchReminderIfDue(ctx context.Context, now time.Time) {
	if now.Hour() < morningHour || !workdays.IsWorkday(now) || !now.Before(workdays.Closing(now)) {
		return
	}
	if ok, _ := b.st.dispatchReminderDue(now.Format("2006-01-02")); !ok {
		return
	}
	list, err := b.undispatched(ctx, now)
	if errors.Is(err, skydropx.ErrMockMode) {
		return
	}
	if err != nil {
		log.Printf("bot: couldn't check what is left to dispatch: %v", err)
		b.sayGestion(ctx, "⚠️ No pude revisar qué falta por despachar: "+html.EscapeString(err.Error())+"\nRevísalo con /despachos más tarde.")
		return
	}
	if len(list) > 0 {
		b.sayGestion(ctx, dispatchText(list, now))
	}
}

func (b *Bot) undispatched(ctx context.Context, now time.Time) ([]skydropx.Undispatched, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return b.sky.Undispatched(ctx, now.Add(-dispatchWindow))
}

// dispatchCommand handles /despachos.
func (b *Bot) dispatchCommand(ctx context.Context, chatID int64) {
	b.say(ctx, chatID, "⏳ Revisando qué guías no se han llevado a Interrapidísimo…")
	now := time.Now().In(colombiaTZ)
	list, err := b.undispatched(ctx, now)
	if errors.Is(err, skydropx.ErrMockMode) {
		b.say(ctx, chatID, "El bot no está conectado a Skydropx (modo de prueba).")
		return
	}
	if err != nil {
		b.say(ctx, chatID, "⚠️ No pude revisar las guías: "+html.EscapeString(err.Error()))
		return
	}
	b.say(ctx, chatID, dispatchText(list, now))
}

// dispatchText lists the labels to take to the carrier, oldest first, and when the office is open.
func dispatchText(list []skydropx.Undispatched, now time.Time) string {
	var b strings.Builder
	if len(list) == 0 {
		fmt.Fprintf(&b, "✅ <b>Nada por despachar</b>\nInterrapidísimo ya recibió todas las guías de los últimos %d días.\n\n", int(dispatchWindow.Hours()/24))
		b.WriteString(officeLine(now))
		return b.String()
	}
	b.WriteString("📦 <b>POR DESPACHAR</b>\n")
	if len(list) == 1 {
		b.WriteString("Hay <b>1</b> guía creada que todavía no se ha llevado a Interrapidísimo:\n\n")
	} else {
		fmt.Fprintf(&b, "Hay <b>%d</b> guías creadas que todavía no se han llevado a Interrapidísimo:\n\n", len(list))
	}
	for _, u := range list {
		name := u.Name
		if name == "" {
			name = "Sin nombre"
		}
		place := strings.Trim(u.City+", "+u.Department, ", ")
		fmt.Fprintf(&b, "• <b>%s</b>", html.EscapeString(name))
		if place != "" {
			fmt.Fprintf(&b, " — %s", html.EscapeString(place))
		}
		fmt.Fprintf(&b, "\n   Guía <code>%s</code> · creada %s (%s)", html.EscapeString(u.Tracking), fechaHora(u.CreatedAt), daysAgo(u.CreatedAt, now))
		if u.Unknown {
			b.WriteString("\n   ⚠️ No pude revisar el rastreo: puede que ya esté despachada.")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + officeLine(now))
	return b.String()
}

// officeLine says until when the office is open today or, if it's closed, the next day it opens.
func officeLine(now time.Time) string {
	if closing := workdays.Closing(now); !closing.IsZero() && now.Before(closing) {
		return "🕕 Hoy la oficina atiende hasta las " + horaCorta(closing)
	}
	next := workdays.NextWorkday(now)
	why := "ya cerró"
	switch {
	case workdays.IsHoliday(now):
		why = "no abre hoy (festivo)"
	case now.Weekday() == time.Sunday:
		why = "no abre hoy (domingo)"
	}
	return fmt.Sprintf("🔒 La oficina %s. Próximo día: %s, hasta las %s", why,
		fechaCorta(next.Format("2006-01-02")), horaCorta(workdays.Closing(next)))
}

// horaCorta formats a time of day, e.g. "6:30 p. m." or "5 p. m.".
func horaCorta(t time.Time) string {
	t = t.In(colombiaTZ)
	ampm := "a. m."
	if t.Hour() >= 12 {
		ampm = "p. m."
	}
	h := t.Hour() % 12
	if h == 0 {
		h = 12
	}
	if t.Minute() == 0 {
		return fmt.Sprintf("%d %s", h, ampm)
	}
	return fmt.Sprintf("%d:%02d %s", h, t.Minute(), ampm)
}

// daysAgo says how long ago a day was, in calendar days of Colombia: "hoy", "ayer", "hace 3 días".
func daysAgo(t, now time.Time) string {
	day := func(x time.Time) time.Time {
		x = x.In(colombiaTZ)
		return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, colombiaTZ)
	}
	switch n := int(day(now).Sub(day(t)).Hours() / 24); n {
	case 0:
		return "hoy"
	case 1:
		return "ayer"
	default:
		return fmt.Sprintf("hace %d días", n)
	}
}
