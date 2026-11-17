package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"skydropx-automator/internal/auth"
	"skydropx-automator/internal/bot"
	"skydropx-automator/internal/config"
	"skydropx-automator/internal/customers"
	"skydropx-automator/internal/handlers"
	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
)

func main() {
	cfg := config.Load()
	client := skydropx.NewClient(cfg)
	// Settings (the seller's commission) are stored next to the bot state, on the persistent disk.
	appSettings, err := settings.Load(filepath.Join(filepath.Dir(cfg.BotStatePath), "settings.json"), cfg.CommissionPerTerminal)
	if err != nil {
		log.Fatalf("No se pudieron leer los ajustes: %v", err)
	}
	apiHandler := handlers.NewAPIHandler(cfg, client, appSettings)

	mux := http.NewServeMux()
	apiHandler.RegisterRoutes(mux)

	guard := auth.New(os.Getenv("APP_PASSWORD"))
	mux.HandleFunc("GET /login", guard.LoginPage)
	mux.HandleFunc("POST /login", guard.LoginPage)
	mux.HandleFunc("GET /logout", guard.Logout)

	// The customer database is optional: if MongoDB doesn't respond, the bot keeps working without it.
	var custs *customers.Store
	var customersErr string
	if cfg.MongoURI != "" {
		store, closeMongo, err := customers.OpenMongo(context.Background(), cfg.MongoURI, cfg.MongoDatabase)
		if err != nil {
			log.Printf("Base de clientes desactivada: %v", err)
			customersErr = err.Error()
		} else {
			custs = store
			defer func() { _ = closeMongo(context.Background()) }()
			log.Printf("Base de clientes activa (MongoDB, base %q)", cfg.MongoDatabase)
		}
	} else {
		log.Println("Base de clientes desactivada: MONGODB_URI no está configurada.")
	}

	if cfg.TelegramToken != "" {
		tgBot, err := bot.New(bot.Options{
			Token:            cfg.TelegramToken,
			APIBase:          cfg.TelegramAPIBase,
			AllowedChatIDs:   cfg.TelegramAllowedChatIDs,
			AdminChatIDs:     cfg.TelegramAdminChatIDs,
			OpsChatIDs:       cfg.TelegramOpsChatIDs,
			DefaultUnitPrice: cfg.DefaultUnitPrice,
			StatePath:        cfg.BotStatePath,
			CheckEvery:       time.Duration(cfg.LabelCheckMinutes) * time.Minute,
			Settings:         appSettings,
			Customers:        custs,
			CustomersError:   customersErr,
		}, client)
		if err != nil {
			log.Fatalf("Bot de Telegram: %v", err)
		}
		go tgBot.Run(context.Background())
		log.Printf("Bot de Telegram activo (chats autorizados: %v)", cfg.TelegramAllowedChatIDs)

		if cfg.SkydropxWebhookSecret != "" {
			mux.HandleFunc("POST "+auth.WebhookPath, tgBot.WebhookHandler(cfg.SkydropxWebhookSecret, cfg.SkydropxWebhookHeader))
			log.Printf("Webhook de Skydropx activo en %s", auth.WebhookPath)
		} else {
			log.Println("Webhook de Skydropx desactivado: SKYDROPX_WEBHOOK_SECRET no está configurado (se revisan guías cada GUIA_CHECK_MINUTES).")
		}
	} else {
		log.Println("Bot de Telegram desactivado: TELEGRAM_BOT_TOKEN no está configurado.")
	}

	if !guard.Enabled() {
		log.Println("ADVERTENCIA: APP_PASSWORD no está configurada; la app no pide inicio de sesión.")
	}

	// Serve the frontend's static files
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
