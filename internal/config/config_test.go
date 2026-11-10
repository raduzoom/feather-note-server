package config

import (
	"strings"
	"testing"
)

const secret = "0101010101010101010101010101010101010101010101010101010101010101"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := FromEnv(env(map[string]string{"FEATHER_DATABASE_URL": "postgres://x", "FEATHER_SERVER_SECRET": secret}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8420" || c.S3.Region != "us-east-1" || c.S3.Bucket != "feather" || c.BillingEnabled ||
		c.QuotaBytes != 10737418240 || c.MaxBlobBytes != 104857600 || c.Mail != "log" || c.TrustProxy {
		t.Fatalf("defaults %+v", c)
	}
	if len(c.ServerSecret) != 32 || c.ServerSecret[0] != 1 {
		t.Fatalf("secret %x", c.ServerSecret)
	}
}

func TestReadsEveryVariable(t *testing.T) {
	c, err := FromEnv(env(map[string]string{
		"FEATHER_ADDR": ":9000", "FEATHER_DATABASE_URL": "postgres://db", "FEATHER_S3_ENDPOINT": "http://s3:8333",
		"FEATHER_S3_REGION": "auto", "FEATHER_S3_BUCKET": "b", "FEATHER_S3_ACCESS_KEY": "ak", "FEATHER_S3_SECRET_KEY": "sk",
		"FEATHER_SERVER_SECRET": secret, "FEATHER_BILLING": "on", "FEATHER_QUOTA_BYTES": "5",
		"FEATHER_MAX_BLOB_BYTES": "7", "FEATHER_MAIL": "log", "FEATHER_TRUST_PROXY": "true",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Addr: ":9000", DatabaseURL: "postgres://db", S3: S3Config{Endpoint: "http://s3:8333", Region: "auto", Bucket: "b", AccessKey: "ak", SecretKey: "sk"},
		BillingEnabled: true, QuotaBytes: 5, MaxBlobBytes: 7, Mail: "log", TrustProxy: true}
	c.ServerSecret = nil
	if c.Addr != want.Addr || c.DatabaseURL != want.DatabaseURL || c.S3 != want.S3 || c.BillingEnabled != want.BillingEnabled ||
		c.QuotaBytes != want.QuotaBytes || c.MaxBlobBytes != want.MaxBlobBytes || c.Mail != want.Mail || c.TrustProxy != want.TrustProxy {
		t.Fatalf("got %+v", c)
	}
}

func TestErrorsNameTheVariable(t *testing.T) {
	base := map[string]string{"FEATHER_DATABASE_URL": "postgres://x", "FEATHER_SERVER_SECRET": secret}
	cases := map[string]map[string]string{
		"FEATHER_SERVER_SECRET": {"FEATHER_SERVER_SECRET": ""},
		"FEATHER_DATABASE_URL":  {"FEATHER_DATABASE_URL": ""},
		"FEATHER_BILLING":       {"FEATHER_BILLING": "maybe"},
		"FEATHER_QUOTA_BYTES":   {"FEATHER_QUOTA_BYTES": "-1"},
		"FEATHER_TRUST_PROXY":   {"FEATHER_TRUST_PROXY": "yes please"},
	}
	cases["FEATHER_SERVER_SECRET "] = map[string]string{"FEATHER_SERVER_SECRET": secret[:63]}
	for name, override := range cases {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range override {
			m[k] = v
		}
		_, err := FromEnv(env(m))
		if err == nil || !strings.Contains(err.Error(), strings.TrimSpace(name)) {
			t.Errorf("%s: err %v", name, err)
		}
	}
}

func TestSMTPSettings(t *testing.T) {
	base := map[string]string{"FEATHER_DATABASE_URL": "postgres://x", "FEATHER_SERVER_SECRET": secret, "FEATHER_MAIL": "smtp",
		"FEATHER_SMTP_HOST": "smtp.resend.com", "FEATHER_SMTP_USERNAME": "resend", "FEATHER_SMTP_PASSWORD": "pw", "FEATHER_SMTP_FROM": "codes@example.com"}
	c, err := FromEnv(env(base))
	if err != nil || c.Mail != "smtp" || c.SMTP.Host != "smtp.resend.com" || c.SMTP.Port != 587 || c.SMTP.Username != "resend" ||
		c.SMTP.Password != "pw" || c.SMTP.From != "codes@example.com" {
		t.Fatalf("%+v %v", c.SMTP, err)
	}
	for _, missing := range []string{"FEATHER_SMTP_HOST", "FEATHER_SMTP_USERNAME", "FEATHER_SMTP_PASSWORD", "FEATHER_SMTP_FROM"} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		delete(m, missing)
		if _, err := FromEnv(env(m)); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("%s: %v", missing, err)
		}
	}
	base["FEATHER_MAIL"] = "carrier-pigeon"
	if _, err := FromEnv(env(base)); err == nil || !strings.Contains(err.Error(), "FEATHER_MAIL") {
		t.Errorf("bad mail: %v", err)
	}
}

func TestClientIPHeader(t *testing.T) {
	c, err := FromEnv(env(map[string]string{"FEATHER_DATABASE_URL": "postgres://x", "FEATHER_SERVER_SECRET": secret,
		"FEATHER_CLIENT_IP_HEADER": "Cf-Connecting-Ip"}))
	if err != nil || c.ClientIPHeader != "Cf-Connecting-Ip" {
		t.Fatalf("%q %v", c.ClientIPHeader, err)
	}
}
