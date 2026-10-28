// Command roomtodo-mcp is an MCP server (stdio) for the roomtodo.com room planner,
// for Claude Desktop, Claude Code and Codex.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Dobytchick/roomtodo-mcp/internal/roomtodo"
	"github.com/Dobytchick/roomtodo-mcp/internal/server"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("[roomtodo] ")
	log.SetOutput(os.Stderr) // stdout is the MCP channel

	home, _ := os.UserHomeDir()
	var (
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
	if strings.EqualFold(filepath.Clean(*projectsDir), filepath.Clean(*stateDir)) {
		log.Fatal("--projects-dir must not be the state folder (it holds the token)")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	srv := server.New(server.Config{ProjectsDir: *projectsDir, BrowserLogin: !*noBrowser}, api)
	if err := srv.MCP().Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
