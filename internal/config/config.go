package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds server configuration from environment variables.
type Config struct {
	Addr       string
	DBPath     string
	SessionTTL time.Duration
	BcryptCost int
	LogLevel   slog.Level
}

// Load reads .env if present, then parses environment variables.
func Load() (*Config, error) {
	_ = godotenv.Load()

	addr := getEnv("MPN_ADDR", "127.0.0.1:8080")
	dbPath := getEnv("MPN_DB_PATH", "db/mpnotepad.sqlite")

	ttlHours, err := parseIntEnv("MPN_SESSION_TTL_HOURS", 720)
	if err != nil {
		return nil, fmt.Errorf("MPN_SESSION_TTL_HOURS: %w", err)
	}
	cost, err := parseIntEnv("MPN_BCRYPT_COST", 12)
	if err != nil {
		return nil, fmt.Errorf("MPN_BCRYPT_COST: %w", err)
	}
	if cost < bcryptMinCost || cost > bcryptMaxCost {
		return nil, fmt.Errorf("MPN_BCRYPT_COST must be between %d and %d", bcryptMinCost, bcryptMaxCost)
	}

	level, err := parseLogLevel(getEnv("MPN_LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}

	return &Config{
		Addr:       addr,
		DBPath:     dbPath,
		SessionTTL: time.Duration(ttlHours) * time.Hour,
		BcryptCost: cost,
		LogLevel:   level,
	}, nil
}

const bcryptMinCost = 4
const bcryptMaxCost = 31

func getEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func parseIntEnv(key string, def int) (int, error) {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown MPN_LOG_LEVEL %q", s)
	}
}
