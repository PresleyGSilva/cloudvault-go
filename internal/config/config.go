package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port          string
	DatabaseURL   string
	JWTSecret     string
	MinioEndpoint string
	MinioUser     string
	MinioPass     string
	MinioBucket   string
	MinioUseSSL   bool
}

func Load() Config {
	_ = godotenv.Load()
	c := Config{
		Port:          env("PORT", "8087"),
		DatabaseURL:   env("DATABASE_URL", "postgres://cloudvault:cloudvault@localhost:5440/cloudvault?sslmode=disable"),
		JWTSecret:     env("JWT_SECRET", "dev-secret-change-me"),
		MinioEndpoint: env("MINIO_ENDPOINT", "localhost:9000"),
		MinioUser:     env("MINIO_ACCESS_KEY", "minioadmin"),
		MinioPass:     env("MINIO_SECRET_KEY", "minioadmin"),
		MinioBucket:   env("MINIO_BUCKET", "cloudvault"),
		MinioUseSSL:   env("MINIO_USE_SSL", "false") == "true",
	}
	if c.JWTSecret == "dev-secret-change-me" && os.Getenv("JWT_SECRET") == "" {
		log.Println("WARN: using default JWT_SECRET; set JWT_SECRET in production")
	}
	return c
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
