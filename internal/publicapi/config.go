package publicapi

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DefaultPageSize int
	MaxPageSize     int
	AllowedOrigins  map[string]bool
	AllowAllOrigins bool
}

func LoadConfig() Config {
	defaultSize := envInt("API_DEFAULT_PAGE_SIZE", 20)
	maxSize := envInt("API_MAX_PAGE_SIZE", 100)
	if maxSize < 1 {
		maxSize = 100
	}
	if defaultSize < 1 || defaultSize > maxSize {
		defaultSize = min(20, maxSize)
	}

	origins := make(map[string]bool)
	allowAll := false
	for _, value := range strings.Split(envString("API_ALLOWED_ORIGINS", "*"), ",") {
		origin := strings.TrimSpace(value)
		if origin == "*" {
			allowAll = true
		} else if origin != "" {
			origins[origin] = true
		}
	}
	return Config{DefaultPageSize: defaultSize, MaxPageSize: maxSize, AllowedOrigins: origins, AllowAllOrigins: allowAll}
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return value
}
