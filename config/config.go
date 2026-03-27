package config

import (
	"os"
	"strings"
)

type Config struct {
	Host            string
	Port            string
	LLMProvider     string
	LLMModel        string
	BusinessType    string
	InoutBaseURL    string
	InoutToken      string
	InoutBusinessID string
}

func Load() Config {
	return Config{
		Host:            getEnv("ROUTER_HOST", "0.0.0.0"),
		Port:            getEnv("ROUTER_PORT", "8081"),
		LLMProvider:     getEnv("ROUTER_LLM_PROVIDER", "mock"),
		LLMModel:        getEnv("ROUTER_LLM_MODEL", "mock-router-v1"),
		BusinessType:    getEnv("ROUTER_BUSINESS_TYPE", "restaurante multi-tenant"),
		InoutBaseURL:    getEnv("INOUT_BASE_URL", "https://api01.inoutdelivery.com.co/v1"),
		InoutToken:      getEnv("INOUT_TOKEN", ""),
		InoutBusinessID: getEnv("INOUT_BUSINESS_ID", "dlk.inoutdelivery.com"),
	}
}

func (c Config) Address() string {
	host := strings.TrimSpace(c.Host)
	if host == "" || host == "0.0.0.0" {
		return ":" + c.Port
	}
	return host + ":" + c.Port
}

func getEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
