package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"skydropx-automator/internal/auth"
	"skydropx-automator/internal/bot"
	"skydropx-automator/internal/config"
	"skydropx-automator/internal/handlers"
	"skydropx-automator/internal/skydropx"
)

func main() {
	cfg := config.Load()
	client := skydropx.NewClient(cfg)
	apiHandler := handlers.NewAPIHandler(cfg, client)

	mux := http.NewServeMux()
	apiHandler.RegisterRoutes(mux)

	guard := auth.New(os.Getenv("APP_PASSWORD"))
	mux.HandleFunc("GET /login", guard.LoginPage)
	mux.HandleFunc("POST /login", guard.LoginPage)
	mux.HandleFunc("GET /logout", guard.Logout)

	if cfg.TelegramToken != "" {
		tgBot, err := bot.New(bot.Options{
			Token:          cfg.TelegramToken,
			APIBase:        cfg.TelegramAPIBase,
			AllowedChatIDs: cfg.TelegramAllowedChatIDs,
			AdminChatIDs:   cfg.TelegramAdminChatIDs,
			GestionChatIDs: cfg.TelegramGestionChatIDs,
			DefaultValor:   cfg.DefaultValorUnitario,
			StatePath:      cfg.BotStatePath,
			CheckEvery:     time.Duration(cfg.GuiaCheckMinutes) * time.Minute,
		}, client)
		if err != nil {
			log.Fatalf("Bot de Telegram: %v", err)
		}
		go tgBot.Run(context.Background())
		log.Printf("Bot de Telegram activo (chats autorizados: %v)", cfg.TelegramAllowedChatIDs)

		if cfg.SkydropxWebhookSecret != "" {
			mux.HandleFunc("POST "+auth.WebhookPath, tgBot.WebhookHandler(cfg.SkydropxWebhookSecret, cfg.SkydropxWebhookHeader))
			log.Printf("Webhook de Skydropx activo en %s", auth.WebhookPath)
			log.Println("Webhook de Skydropx desactivado: SKYDROPX_WEBHOOK_SECRET no está configurado (se revisan guías cada GUIA_CHECK_MINUTES).")
		}
	} else {
		log.Println("Bot de Telegram desactivado: TELEGRAM_BOT_TOKEN no está configurado.")
	}

	if !guard.Enabled() {
		log.Println("ADVERTENCIA: APP_PASSWORD no está configurada; la app no pide inicio de sesión.")
	}

	// Servir archivos estáticos del frontend
	webDir := "web"
	if _, err := os.Stat(webDir); os.IsNotExist(err) {
		log.Fatalf("No se encontró el directorio %s. Ejecuta el servidor desde la raíz del proyecto.", webDir)
	}
	mux.Handle("GET /", http.FileServer(http.Dir(webDir)))

	addr := ":" + cfg.Port
	log.Printf("Servidor iniciado ds en http://localhost%s [Modo: %s | Origen: %s, %s | Transportadora: %s]",
		addr, cfg.SkydropxMode, cfg.OriginCity, cfg.OriginState, cfg.PreferredCarrier)

	if err := http.ListenAndServe(addr, guard.Wrap(mux)); err != nil {
		log.Fatalf("Error iniciando servidor HTTP: %v", err)
	}
}
