// Command fairmind-devbridge is a DEVELOPMENT-ONLY local server implementing
// the proposed Agent API v1 on top of the real FairMind MCP server, so the
// fairmind CLI can be tried against real data before the server-side Agent
// API exists. It stores no credentials: it forwards the caller's
// Authorization header to the MCP server. Do not distribute it to customers.
package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/devbridge"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8788", "loopback address to listen on")
	mcpURL := flag.String("mcp-url", "https://project-context.fairmind.ai/mcp/mcp", "FairMind MCP endpoint (Streamable HTTP)")
	flag.Parse()

	host, _, err := net.SplitHostPort(*listen)
	if ip := net.ParseIP(host); err != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		log.Fatalf("refusing to listen on %q: only loopback addresses are allowed", *listen)
	}
	u, err := url.Parse(*mcpURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		log.Fatalf("--mcp-url must be an https URL without credentials")
	}

	logger := log.New(os.Stderr, "fairmind-devbridge ", log.LstdFlags)
	srv := &http.Server{
		Addr:              *listen,
		Handler:           devbridge.NewServer(devbridge.NewMCPClient(u.String()), logger),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      120 * time.Second,
	}
	logger.Printf("listening on http://%s -> %s (development only)", *listen, u.Host)
	logger.Fatal(srv.ListenAndServe())
}
