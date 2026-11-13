// Package auth protege la app con una contraseña única (variable APP_PASSWORD).
// Si APP_PASSWORD no está configurada, la app no pide inicio de sesión (uso local).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	cookieName = "sd_session"
	sessionTTL = 12 * time.Hour
	loginDelay = 700 * time.Millisecond // frena intentos de adivinar la contraseña
)

type Guard struct {
	password string
	mu       sync.Mutex
	sessions map[string]time.Time
}

func New(password string) *Guard {
	return &Guard{password: password, sessions: make(map[string]time.Time)}
}

func (g *Guard) Enabled() bool { return g.password != "" }

func (g *Guard) newSession() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for k, exp := range g.sessions { // limpia sesiones vencidas
		if now.After(exp) {
			delete(g.sessions, k)
		}
	}
	g.sessions[token] = now.Add(sessionTTL)
	return token, nil
}

func (g *Guard) isAuthenticated(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	exp, ok := g.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(g.sessions, c.Value)
		return false
	}
	return true
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (g *Guard) setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// Wrap exige sesión para todo excepto /login.
func (g *Guard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.Enabled() || r.URL.Path == "/login" || r.URL.Path == "/logout" || g.isAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Tu sesión expiró. Vuelve a iniciar sesión."})
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

// LoginPage muestra el formulario (GET) o valida la contraseña (POST).
func (g *Guard) LoginPage(w http.ResponseWriter, r *http.Request) {
	if !g.Enabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if r.Method == http.MethodPost {
		time.Sleep(loginDelay)
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/login?error=1", http.StatusFound)
			return
		}
		given := []byte(r.PostFormValue("password"))
		if subtle.ConstantTimeCompare(given, []byte(g.password)) != 1 {
			http.Redirect(w, r, "/login?error=1", http.StatusFound)
			return
		}
		token, err := g.newSession()
		if err != nil {
			http.Error(w, "No se pudo iniciar sesión", http.StatusInternalServerError)
			return
		}
		g.setCookie(w, r, token, int(sessionTTL.Seconds()))
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	errorBox := ""
	if r.URL.Query().Get("error") == "1" {
		errorBox = `<p class="mb-4 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">Contraseña incorrecta.</p>`
	}
	_, _ = w.Write([]byte(loginHTML(errorBox)))
}

// Logout cierra la sesión actual.
func (g *Guard) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		g.mu.Lock()
		delete(g.sessions, c.Value)
		g.mu.Unlock()
	}
	g.setCookie(w, r, "", -1)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func loginHTML(errorBox string) string {
	return `<!DOCTYPE html>
<html lang="es">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Entrar · Skydropx</title>
  <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="flex min-h-screen items-center justify-center bg-slate-50 px-4 font-sans text-slate-800">
  <main class="w-full max-w-sm rounded-2xl bg-white p-8 shadow-sm ring-1 ring-slate-900/5">
    <div class="mb-6 flex items-center gap-3">
      <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-to-br from-indigo-600 to-violet-600 text-white">
        <svg viewBox="0 0 24 24" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 8 12 3 3 8v8l9 5 9-5V8Z"/><path d="m3.3 7.3 8.7 4.7 8.7-4.7M12 22V12"/></svg>
      </div>
      <div>
        <h1 class="text-base font-bold text-slate-900">Skydropx</h1>
        <p class="text-xs text-slate-500">Acceso privado</p>
      </div>
    </div>
    ` + errorBox + `
    <form method="POST" action="/login" class="space-y-4">
      <div>
        <label for="password" class="mb-1.5 block text-sm font-medium text-slate-700">Contraseña</label>
        <input type="password" id="password" name="password" required autofocus autocomplete="current-password"
          class="w-full rounded-xl border border-slate-200 px-3.5 py-2.5 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-4 focus:ring-indigo-100" />
      </div>
      <button type="submit" class="w-full rounded-xl bg-gradient-to-r from-indigo-600 to-violet-600 px-4 py-2.5 text-sm font-semibold text-white shadow-lg shadow-indigo-500/25 hover:brightness-110">Entrar</button>
    </form>
  </main>
</body>
</html>`
}
