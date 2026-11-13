// Package config reads the server's settings from the environment.
package config

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// S3Config locates the blob store (SeaweedFS locally, Cloudflare R2 hosted, any S3 for self-hosters).
type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
}

// SMTPConfig is the mail provider when Mail is "smtp".
type SMTPConfig struct {
	Host, Username, Password, From string
	Port                           int
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
	Mail           string // "log" or "smtp"
	SMTP           SMTPConfig
	TrustProxy     bool     // use the last X-Forwarded-For hop as the client IP
	AllowedOrigins []string // browser origins allowed by CORS; empty: no CORS headers
	ClientIPHeader string   // with TrustProxy: a header holding the visitor's address (Cf-Connecting-Ip behind Cloudflare)
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
		Mail:           get("FEATHER_MAIL", "log"),
		ClientIPHeader: getenv("FEATHER_CLIENT_IP_HEADER"),
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
	if raw := strings.TrimSpace(getenv("FEATHER_ALLOWED_ORIGINS")); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			o = strings.TrimSpace(o)
			if err := checkOrigin(o); err != nil {
				return c, fmt.Errorf("FEATHER_ALLOWED_ORIGINS: %v", err)
			}
			c.AllowedOrigins = append(c.AllowedOrigins, o)
		}
	}
	switch c.Mail {
	case "log":
	case "smtp":
		c.SMTP = SMTPConfig{Host: getenv("FEATHER_SMTP_HOST"), Username: getenv("FEATHER_SMTP_USERNAME"),
			Password: getenv("FEATHER_SMTP_PASSWORD"), From: getenv("FEATHER_SMTP_FROM")}
		for name, v := range map[string]string{"FEATHER_SMTP_HOST": c.SMTP.Host, "FEATHER_SMTP_USERNAME": c.SMTP.Username,
			"FEATHER_SMTP_PASSWORD": c.SMTP.Password, "FEATHER_SMTP_FROM": c.SMTP.From} {
			if v == "" {
				return c, fmt.Errorf("%s is required when FEATHER_MAIL=smtp", name)
			}
		}
		port, err := positive(get("FEATHER_SMTP_PORT", "587"))
		if err != nil || port > 65535 {
			return c, fmt.Errorf("FEATHER_SMTP_PORT must be a port number")
		}
		c.SMTP.Port = int(port)
	default:
		return c, fmt.Errorf("FEATHER_MAIL must be log or smtp, not %q", c.Mail)
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

// checkOrigin accepts only scheme://host[:port]: no path, query, user, wildcard or trailing slash.
func checkOrigin(o string) error {
	u, err := url.Parse(o)
	bad := fmt.Errorf("%q must be an origin like https://app.example.com (scheme, host, optional port; no path or wildcard)", o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		strings.ContainsAny(o, "*? #") || o != u.Scheme+"://"+u.Host {
		return bad
	}
	return nil
}
