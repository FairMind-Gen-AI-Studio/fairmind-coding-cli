// Command fairmind-mock serves the in-memory mock of the FairMind Agent API
// for local development and demos. It is NOT the FairMind backend.
//
//	fairmind-mock serve [--addr 127.0.0.1:8787]
//	fairmind-mock mint  [--scope read] [--ttl 8h] [--company acme] [--project <id>]
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/mock"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		addr := fs.String("addr", "127.0.0.1:8787", "listen address (loopback only is recommended)")
		_ = fs.Parse(os.Args[2:])
		srv := &http.Server{Addr: *addr, Handler: mock.New(), ReadHeaderTimeout: 5 * time.Second}
		log.Printf("fairmind-mock listening on http://%s (mock data only)", *addr)
		log.Fatal(srv.ListenAndServe())
	case "mint":
		fs := flag.NewFlagSet("mint", flag.ExitOnError)
		scope := fs.String("scope", "read", "space-separated scopes")
		ttl := fs.Duration("ttl", 8*time.Hour, "token lifetime (negative = already expired)")
		company := fs.String("company", mock.Tenant, "tenant claim")
		project := fs.String("project", "", "optional project-scope claim")
		sub := fs.String("sub", "dev-user", "subject claim")
		_ = fs.Parse(os.Args[2:])
		// Unsigned test token for the mock only; print it so it can be piped
		// into `fairmind auth login` or exported in a test shell.
		fmt.Println(mock.MintToken(mock.Claims{Subject: *sub, Company: *company, Scope: *scope,
			Project: *project, Exp: time.Now().Add(*ttl).Unix(), Issuer: "fairmind-mock"}))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: fairmind-mock serve [--addr host:port] | mint [--scope read] [--ttl 8h] [--company acme] [--project id]")
	os.Exit(2)
}
