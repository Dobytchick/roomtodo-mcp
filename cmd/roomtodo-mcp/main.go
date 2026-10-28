// Command roomtodo-mcp is an MCP server for the roomtodo.com room planner.
//
//	roomtodo-mcp                      stdio, for Claude Desktop / Claude Code
//	roomtodo-mcp --http :8080         Streamable HTTP, for ChatGPT (behind an HTTPS tunnel)
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Dobytchick/roomtodo-mcp/internal/roomtodo"
	"github.com/Dobytchick/roomtodo-mcp/internal/server"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("[roomtodo] ")
	log.SetOutput(os.Stderr) // stdout is the MCP channel in stdio mode

	home, _ := os.UserHomeDir()
	var (
		httpAddr    = flag.String("http", "", "serve Streamable HTTP on this address (e.g. :8080) instead of stdio")
		publicURL   = flag.String("public-url", env("ROOMTODO_PUBLIC_URL", ""), "public base URL in HTTP mode (e.g. your tunnel URL), for download links")
		secret      = flag.String("secret", env("ROOMTODO_SECRET", ""), "path secret guarding the HTTP endpoint (default: random); \"none\" disables it")
		stateDir    = flag.String("state-dir", env("ROOMTODO_STATE_DIR", filepath.Join(home, ".roomtodo-mcp")), "where the token and the sign-in browser profile are kept")
		projectsDir = flag.String("projects-dir", env("ROOMTODO_PROJECTS_DIR", ""), "folder for local project files (default: <state-dir>/projects)")
		token       = flag.String("token", os.Getenv("ROOMTODO_TOKEN"), "roomtodo token (skips the browser sign-in)")
		lang        = flag.String("lang", env("ROOMTODO_LANG", "ru"), "catalog language (ru, en, ...)")
		browser     = flag.String("browser", os.Getenv("ROOMTODO_BROWSER"), "Chrome/Edge executable for the sign-in window")
		noBrowser   = flag.Bool("no-browser-login", os.Getenv("ROOMTODO_NO_BROWSER_LOGIN") != "", "disable the interactive browser sign-in")
		version     = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()
	if *version {
		fmt.Println("roomtodo-mcp", server.Version)
		return
	}
	if *projectsDir == "" {
		*projectsDir = filepath.Join(*stateDir, "projects")
	}
	if err := os.MkdirAll(*projectsDir, 0o755); err != nil {
		log.Fatal(err)
	}

	api := roomtodo.New(*token, &roomtodo.TokenStore{Dir: *stateDir})
	api.Lang = *lang
	api.Log = func(s string) { log.Print(s) }
	if d := os.Getenv("ROOMTODO_DOMAIN"); d != "" {
		api.Domain = d
	}
	if !*noBrowser {
		bl := &roomtodo.BrowserLogin{Domain: api.Domain, SiteKey: api.SiteKey, StateDir: *stateDir, Browser: *browser,
			Log: func(s string) { log.Print(s) }}
		api.Login = bl.Token
	}

	cfg := server.Config{ProjectsDir: *projectsDir, HTTPMode: *httpAddr != "", PublicURL: *publicURL, BrowserLogin: !*noBrowser}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *httpAddr == "" {
		srv := server.New(cfg, api)
		if err := srv.MCP().Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatal(err)
		}
		return
	}

	switch *secret {
	case "none":
		*secret = ""
	case "":
		b := make([]byte, 12)
		rand.Read(b)
		*secret = hex.EncodeToString(b)
	}
	cfg.Secret = *secret
	srv := server.New(cfg, api)
	hs := &http.Server{Addr: *httpAddr, Handler: srv.HTTPHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()

	path := "/mcp"
	if cfg.Secret != "" {
		path = "/" + cfg.Secret + "/mcp"
	}
	base := strings.TrimRight(*publicURL, "/")
	if base == "" {
		host := *httpAddr
		if strings.HasPrefix(host, ":") {
			host = "localhost" + host
		}
		base = "http://" + host
	}
	log.Printf("MCP endpoint: %s%s", base, path)
	if *publicURL == "" {
		log.Printf("for ChatGPT, expose it over HTTPS, e.g.: cloudflared tunnel --url http://localhost%s  — then use https://<tunnel-host>%s", portOf(*httpAddr), path)
	}
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":" + addr
}
