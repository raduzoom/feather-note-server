// Command feather-server runs the Feather Cloud server.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"feathernote/server/internal/api"
	"feathernote/server/internal/blobs"
	"feathernote/server/internal/config"
	"feathernote/server/internal/janitor"
	"feathernote/server/internal/mail"
	"feathernote/server/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("feather-server", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the version and exit")
	healthcheck := fs.Bool("healthcheck", false, "exit 0 if the server at -addr answers /healthz (for container health checks)")
	defaultAddr := os.Getenv("FEATHER_ADDR")
	if defaultAddr == "" {
		defaultAddr = ":8420"
	}
	addr := fs.String("addr", defaultAddr, "listen address (env FEATHER_ADDR)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "feather-server %s\n", version)
		return 0
	}
	if *healthcheck {
		return probe(*addr, stderr)
	}

	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "feather-server: %v\n", err)
		return 1
	}
	cfg.Addr = *addr
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "feather-server: listen %s: %v\n", *addr, err)
		return 1
	}
	defer ln.Close()
	ctx := context.Background()
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "feather-server: %v\n", err)
		return 1
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		fmt.Fprintf(stderr, "feather-server: %v\n", err)
		return 1
	}
	storage, err := blobs.NewS3(cfg.S3)
	if err == nil {
		err = storage.EnsureBucket(ctx)
	}
	if err != nil {
		fmt.Fprintf(stderr, "feather-server: blob storage: %v\n", err)
		return 1
	}
	j := &janitor.Janitor{DB: db, Storage: storage, Clock: time.Now, Log: log}
	go func() {
		for {
			if err := j.Run(ctx); err != nil {
				log.Error("janitor", "type", fmt.Sprintf("%T", err))
			}
			time.Sleep(10 * time.Minute)
		}
	}()
	fmt.Fprintf(stderr, "feather-server %s listening on %s (billing %v)\n", version, ln.Addr(), cfg.BillingEnabled)
	mailer := mail.NewLog(stdout)
	if cfg.Mail == "smtp" {
		mailer = mail.NewSMTP(mail.SMTPConfig{Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, Username: cfg.SMTP.Username,
			Password: cfg.SMTP.Password, From: cfg.SMTP.From})
	}
	handler := api.NewRouter(api.Deps{Config: cfg, DB: db, Mailer: mailer, Storage: storage, Clock: time.Now, Log: log, Version: version})
	if err := newHTTPServer(handler).Serve(ln); err != nil {
		fmt.Fprintf(stderr, "feather-server: %v\n", err)
		return 1
	}
	return 0
}

// probe is the container health check: distroless images have no shell or wget.
func probe(addr string, stderr io.Writer) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(stderr, "feather-server: %v\n", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		fmt.Fprintf(stderr, "feather-server: healthcheck: %v\n", err)
		return 1
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		return 1
	}
	return 0
}

// newHTTPServer limits how long a client may take to send headers and keep an idle connection; request bodies
// get per-route deadlines in api (blob uploads longer than JSON).
func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}
}
