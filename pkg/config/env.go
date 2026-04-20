package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// ArchivePublishDelay is the expected lag between an hour ending and
// GH Archive publishing the corresponding file (~2 hours).
const ArchivePublishDelay = 2 * time.Hour

func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func GetEnvAsInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil || i < 1 {
		return fallback
	}
	return i
}

func GetEnvBool(key string) bool {
	v := strings.ToLower(os.Getenv(key))
	return v == "true" || v == "1"
}

func GetEnvAsFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func DebugEnabled() bool {
	return GetEnvBool("DEVTRACE_DEBUG")
}
