package config

import (
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

type Config struct {
	Environment        string
	Port               string
	Version            string

	// Database
	DatabaseURL        string

	// Redis
	RedisURL           string

	// Auth
	JWTSecret          string
	GoogleClientID     string

	// Firebase
	FCMCredentialsPath string

	// CORS
	AllowedOrigins     []string

	// Coturn
	TurnUsername       string
	TurnCredential     string
}

func Load() *Config {
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Defaults
	viper.SetDefault("PORT",        "8080")
	viper.SetDefault("ENVIRONMENT", "development")
	viper.SetDefault("VERSION",     "1.0.0")
	viper.SetDefault("ALLOWED_ORIGINS", "http://localhost:3000")

	cfg := &Config{
		Environment:        getEnv("ENVIRONMENT", "development"),
		Port:               getEnv("PORT", "8080"),
		Version:            getEnv("VERSION", "1.0.0"),
		DatabaseURL:        mustEnv("DATABASE_URL"),
		RedisURL:           mustEnv("REDIS_URL"),
		JWTSecret:          mustEnv("JWT_SECRET"),
		GoogleClientID:     mustEnv("GOOGLE_CLIENT_ID"),
		FCMCredentialsPath: getEnv("FCM_CREDENTIALS_PATH", "/secrets/firebase-adminsdk.json"),
		AllowedOrigins:     strings.Split(getEnv("ALLOWED_ORIGINS", "http://localhost:3000"), ","),
		TurnUsername:       getEnv("TURN_USERNAME", "chatbook"),
		TurnCredential:     getEnv("TURN_CREDENTIAL", "change_me"),
	}

	log.Info().
		Str("environment", cfg.Environment).
		Str("port", cfg.Port).
		Msg("Configuration loaded")

	return cfg
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func mustEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		log.Fatal().Str("key", key).Msg("Required environment variable not set")
	}
	return val
}
