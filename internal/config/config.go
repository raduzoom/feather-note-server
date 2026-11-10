// Package config reads the server's settings from the environment.
package config

import (
	"encoding/hex"
	"fmt"
	"strconv"
)

// S3Config locates the blob store (SeaweedFS locally, Cloudflare R2 hosted, any S3 for self-hosters).
type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
}

// Config is everything the server reads at start.
type Config struct {
	Addr           string
	DatabaseURL    string
	S3             S3Config
	ServerSecret   []byte // 32 bytes; keys the fake prelogin salt
	BillingEnabled bool
	QuotaBytes     int64 // every account's quota when billing is off
	MaxBlobBytes   int64
	Mail           string // "log"
	TrustProxy     bool   // use the last X-Forwarded-For hop as the client IP
}

// FromEnv reads FEATHER_* variables; errors name the variable at fault.
func FromEnv(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	c := Config{
		Addr:        get("FEATHER_ADDR", ":8420"),
		DatabaseURL: getenv("FEATHER_DATABASE_URL"),
		S3: S3Config{
			Endpoint:  getenv("FEATHER_S3_ENDPOINT"),
			Region:    get("FEATHER_S3_REGION", "us-east-1"),
			Bucket:    get("FEATHER_S3_BUCKET", "feather"),
			AccessKey: getenv("FEATHER_S3_ACCESS_KEY"),
			SecretKey: getenv("FEATHER_S3_SECRET_KEY"),
		},
		Mail: get("FEATHER_MAIL", "log"),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("FEATHER_DATABASE_URL is required")
	}
	secret, err := hex.DecodeString(getenv("FEATHER_SERVER_SECRET"))
	if err != nil || len(secret) != 32 {
		return c, fmt.Errorf("FEATHER_SERVER_SECRET must be 64 hex characters (generate one with: openssl rand -hex 32)")
	}
	c.ServerSecret = secret
	switch b := get("FEATHER_BILLING", "off"); b {
	case "on":
		c.BillingEnabled = true
	case "off":
	default:
		return c, fmt.Errorf("FEATHER_BILLING must be on or off, not %q", b)
	}
	if c.QuotaBytes, err = positive(get("FEATHER_QUOTA_BYTES", "10737418240")); err != nil {
		return c, fmt.Errorf("FEATHER_QUOTA_BYTES: %v", err)
	}
	if c.MaxBlobBytes, err = positive(get("FEATHER_MAX_BLOB_BYTES", "104857600")); err != nil {
		return c, fmt.Errorf("FEATHER_MAX_BLOB_BYTES: %v", err)
	}
	if c.TrustProxy, err = strconv.ParseBool(get("FEATHER_TRUST_PROXY", "false")); err != nil {
		return c, fmt.Errorf("FEATHER_TRUST_PROXY must be true or false")
	}
	if c.Mail != "log" {
		return c, fmt.Errorf("FEATHER_MAIL must be log")
	}
	return c, nil
}

func positive(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("must be a positive number of bytes, not %q", s)
	}
	return n, nil
}
