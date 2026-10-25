package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL, ValkeyURL, Listen, AdminPath, BootstrapToken, CookieSecure, MasterKeyFile string
	SessionTTL                                                                             time.Duration
	LoginRate, SubscriptionRate, NodeRate, AdminRate                                       int64
	RateWindow                                                                             time.Duration
}

func Load() (Config, error) {
	c := Config{DatabaseURL: secret("DATABASE_URL"), ValkeyURL: secretDefault("VALKEY_URL", "redis://valkey:6379/0"), Listen: get("LISTEN_ADDR", ":8080"), AdminPath: strings.Trim(secret("ADMIN_PATH"), "/"), BootstrapToken: secret("BOOTSTRAP_TOKEN"), CookieSecure: get("COOKIE_SECURE", "true"), MasterKeyFile: get("MASTER_KEY_FILE", "secrets/master_key"), SessionTTL: 24 * time.Hour, LoginRate: integer("LOGIN_RATE_LIMIT", 10), SubscriptionRate: integer("SUBSCRIPTION_RATE_LIMIT", 120), NodeRate: integer("NODE_RATE_LIMIT", 300), AdminRate: integer("ADMIN_RATE_LIMIT", 600), RateWindow: duration("RATE_LIMIT_WINDOW", time.Minute)}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL required")
	}
	if len(c.AdminPath) < 16 {
		return c, errors.New("ADMIN_PATH must be at least 16 characters")
	}
	return c, nil
}
func get(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func integer(k string, d int64) int64 {
	v, e := strconv.ParseInt(get(k, fmt.Sprint(d)), 10, 64)
	if e != nil || v < 0 {
		return d
	}
	return v
}
func duration(k string, d time.Duration) time.Duration {
	v, e := time.ParseDuration(get(k, d.String()))
	if e != nil || v <= 0 {
		return d
	}
	return v
}
func secretDefault(k, d string) string {
	if v := secret(k); v != "" {
		return v
	}
	return d
}
func secret(k string) string {
	if p := os.Getenv(k + "_FILE"); p != "" {
		b, e := os.ReadFile(p)
		if e == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return os.Getenv(k)
}
