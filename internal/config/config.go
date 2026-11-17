package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port                 string
	SkydropxMode         string // "mock" or "live"
	SkydropxBaseURL      string // host without /api/v1, e.g. https://pro.skydropx.com
	SkydropxClientID     string // OAuth client_id (Conexiones > API in Skydropx)
	SkydropxClientSecret string // OAuth client_secret

	// Skydropx webhook (Conexiones > Webhooks): HMAC key or Bearer token, and header name.
	SkydropxWebhookSecret string
	SkydropxWebhookHeader string

	// Customer database in MongoDB (Atlas). Without MONGODB_URI no customers are stored.
	MongoURI      string
	MongoDatabase string

	// Origin address saved in Skydropx (Mis direcciones): by ID or, failing that, by alias.
	OriginAddressTemplateID string
	OriginAddressAlias      string

	OriginName           string
	OriginCompany        string
	OriginStreet         string
	OriginInternalNumber string
	OriginReference      string
	OriginNeighborhood   string
	OriginCity           string
	OriginState          string
	OriginCountry        string
	OriginPostalCode     string
	OriginPhone          string
	OriginEmail          string

	PreferredCarrier string
	DefaultWeightKg  float64
	DefaultLengthCm  int
	DefaultWidthCm   int
	DefaultHeightCm  int
	DefaultContent   string
	// Telegram bot (seller)
	TelegramToken          string
	TelegramAllowedChatIDs []int64
	TelegramAdminChatIDs   []int64 // group where the ready label is posted
	TelegramOpsChatIDs     []int64 // your private group: new orders with a button to generate the label and /imprimir
	TelegramAPIBase        string
	DefaultUnitPrice       float64
	CommissionPerTerminal  float64 // what the seller is paid for each terminal sold
	BotStatePath           string
	LabelCheckMinutes      int
}

func Load() Config {
	loadDotEnv(".env")

	return Config{
		Port:                 getEnv("PORT", "8080"),
		SkydropxMode:         strings.ToLower(getEnv("SKYDROPX_MODE", "mock")),
		SkydropxBaseURL:      strings.TrimRight(getEnv("SKYDROPX_BASE_URL", "https://pro.skydropx.com"), "/"),
		SkydropxClientID:     getEnv("SKYDROPX_CLIENT_ID", ""),
		SkydropxClientSecret: getEnv("SKYDROPX_CLIENT_SECRET", ""),

		SkydropxWebhookSecret: getEnv("SKYDROPX_WEBHOOK_SECRET", ""),
		SkydropxWebhookHeader: getEnv("SKYDROPX_WEBHOOK_HEADER", "Authorization"),

		MongoURI:      getEnv("MONGODB_URI", ""),
		MongoDatabase: getEnv("MONGODB_DB", "skydropx"),

		OriginAddressTemplateID: getEnv("ORIGIN_ADDRESS_TEMPLATE_ID", ""),
		OriginAddressAlias:      getEnv("ORIGIN_ADDRESS_ALIAS", "belu"),

		// Origin: verified address of the Belu Store branch in Skydropx (branch ID 232574).
		OriginName:           getEnv("ORIGIN_NAME", "Kevin Dorado"),
		OriginCompany:        getEnv("ORIGIN_COMPANY", "belu store"),
		OriginStreet:         getEnv("ORIGIN_STREET", "CL 24A N57A 84"),
		OriginInternalNumber: getEnv("ORIGIN_INTERNAL_NUMBER", "502"),
		OriginReference:      getEnv("ORIGIN_REFERENCE", "apto 502 edificio montecarlo"),
		OriginNeighborhood:   getEnv("ORIGIN_NEIGHBORHOOD", "Cabañas"),
		OriginCity:           getEnv("ORIGIN_CITY", "Bello"),
		OriginState:          getEnv("ORIGIN_STATE", "Antioquia"),
		OriginCountry:        getEnv("ORIGIN_COUNTRY", "CO"),
		OriginPostalCode:     getEnv("ORIGIN_POSTAL_CODE", "051051"),
		OriginPhone:          getEnv("ORIGIN_PHONE", "3006368229"),
		OriginEmail:          getEnv("ORIGIN_EMAIL", "leadmarket07@gmail.com"),

		PreferredCarrier: getEnv("PREFERRED_CARRIER", "Interrapidísimo"),
		DefaultWeightKg:  getEnvFloat("DEFAULT_WEIGHT_KG", 1.0),
		// "datafono" package from Mis paquetes in Skydropx: 10 x 10 x 15 cm, 1 kg.
		DefaultLengthCm:        getEnvInt("DEFAULT_LENGTH_CM", 10),
		DefaultWidthCm:         getEnvInt("DEFAULT_WIDTH_CM", 10),
		DefaultHeightCm:        getEnvInt("DEFAULT_HEIGHT_CM", 15),
		DefaultContent:         getEnv("DEFAULT_CONTENT", "datafono"),
		TelegramToken:          getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramAllowedChatIDs: getEnvInt64List("TELEGRAM_ALLOWED_CHAT_IDS", "TELEGRAM_ALLOWED_CHAT_ID"),
		TelegramAdminChatIDs:   getEnvInt64List("TELEGRAM_ADMIN_CHAT_IDS"),
		TelegramOpsChatIDs:     getEnvInt64List("TELEGRAM_GESTION_CHAT_IDS"),
		TelegramAPIBase:        getEnv("TELEGRAM_API_BASE", ""),
		DefaultUnitPrice:       getEnvFloat("DEFAULT_VALOR_UNITARIO", 60000),
		CommissionPerTerminal:  getEnvFloat("COMISION_POR_DATAFONO", 15000),
		BotStatePath:           getEnv("BOT_STATE_FILE", "data/bot_state.json"),
		LabelCheckMinutes:      getEnvInt("GUIA_CHECK_MINUTES", 5),
	}
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return fallback
}

// getEnvInt64List reads comma-separated IDs (TELEGRAM_ALLOWED_CHAT_IDS=123,456).
// It also accepts the old single-ID variable as a fallback.
func getEnvInt64List(keys ...string) []int64 {
	var out []int64
	for _, key := range keys {
		for _, part := range strings.Split(os.Getenv(key), ",") {
			if i, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && i != 0 {
				out = append(out, i)
			}
		}
	}
	return out
}

func getEnvInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// loadDotEnv loads a simple .env file without external dependencies.
func loadDotEnv(filename string) {
	f, err := os.Open(filename)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
	}
}
