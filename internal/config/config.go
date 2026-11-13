package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port                 string
	SkydropxMode         string // "mock" o "live"
	SkydropxBaseURL      string // host sin /api/v1, p. ej. https://pro.skydropx.com
	SkydropxClientID     string // OAuth client_id (Conexiones > API en Skydropx)
	SkydropxClientSecret string // OAuth client_secret

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
	// Bot de Telegram (vendedor)
	TelegramToken          string
	TelegramAllowedChatIDs []int64
	TelegramAPIBase        string
	DefaultValorUnitario   float64
	BotStatePath           string
	GuiaCheckMinutes       int
}

func Load() Config {
	loadDotEnv(".env")

	return Config{
		Port:                 getEnv("PORT", "8080"),
		SkydropxMode:         strings.ToLower(getEnv("SKYDROPX_MODE", "mock")),
		SkydropxBaseURL:      strings.TrimRight(getEnv("SKYDROPX_BASE_URL", "https://pro.skydropx.com"), "/"),
		SkydropxClientID:     getEnv("SKYDROPX_CLIENT_ID", ""),
		SkydropxClientSecret: getEnv("SKYDROPX_CLIENT_SECRET", ""),

		// Origen: dirección verificada de la sucursal Belu Store en Skydropx (ID sucursal 232574).
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
		// Paquete "datafono" de Mis paquetes en Skydropx: 10 x 10 x 15 cm, 1 kg.
		DefaultLengthCm:        getEnvInt("DEFAULT_LENGTH_CM", 10),
		DefaultWidthCm:         getEnvInt("DEFAULT_WIDTH_CM", 10),
		DefaultHeightCm:        getEnvInt("DEFAULT_HEIGHT_CM", 15),
		DefaultContent:         getEnv("DEFAULT_CONTENT", "datafono"),
		TelegramToken:          getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramAllowedChatIDs: getEnvInt64List("TELEGRAM_ALLOWED_CHAT_IDS", "TELEGRAM_ALLOWED_CHAT_ID"),
		TelegramAPIBase:        getEnv("TELEGRAM_API_BASE", ""),
		DefaultValorUnitario:   getEnvFloat("DEFAULT_VALOR_UNITARIO", 60000),
		BotStatePath:           getEnv("BOT_STATE_FILE", "data/bot_state.json"),
		GuiaCheckMinutes:       getEnvInt("GUIA_CHECK_MINUTES", 5),
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

// getEnvInt64List lee IDs separados por comas (TELEGRAM_ALLOWED_CHAT_IDS=123,456).
// También acepta la variable anterior de un solo ID como respaldo.
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

// loadDotEnv carga un archivo .env sencillo sin dependencias externas.
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
