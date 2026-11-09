// Command feather-server runs the Feather Cloud server.
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	"feathernote/server/internal/api"
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

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "feather-server: listen %s: %v\n", *addr, err)
		return 1
	}
	fmt.Fprintf(stderr, "feather-server %s listening on %s\n", version, ln.Addr())
	if err := http.Serve(ln, api.NewRouter(version)); err != nil {
		fmt.Fprintf(stderr, "feather-server: %v\n", err)
		return 1
	}
	return 0
}
