package config

import (
	"os"
)

type Config struct {
	Port      string
	DBType    string // sqlite, mysql
	DBPath    string
	JWTSecret string
	LogLevel  string
}

func Load() *Config {
	return &Config{
		Port:      getEnv("PORT", "3300"),
		DBType:    getEnv("DB_TYPE", "sqlite"),
		DBPath:    getEnv("DB_PATH", "./data/atmapi.db"),
		JWTSecret: getEnv("JWT_SECRET", "atmapi-secret-key"),
		LogLevel:  getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
