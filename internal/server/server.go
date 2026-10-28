// Package server exposes roomtodo as MCP tools.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Dobytchick/roomtodo-mcp/internal/roomtodo"
)

// Version is set at build time (-ldflags "-X .../server.Version=v1.2.3").
var Version = "dev"

// Config configures the server.
type Config struct {
	// ProjectsDir holds local project files (relative names resolve here).
	ProjectsDir string
	// HTTPMode restricts file access to ProjectsDir and enables download links.
	HTTPMode bool
	// PublicURL is the externally visible base URL in HTTP mode (e.g. a tunnel URL), used for
	// download links. Empty means derive it from the request.
	PublicURL string
	// Secret, if set, is the path prefix that guards the HTTP endpoints: /<secret>/mcp.
	Secret string
	// BrowserLogin enables the interactive browser sign-in.
	BrowserLogin bool
}

// Server holds the shared state of all MCP sessions.
type Server struct {
	cfg Config
	api *roomtodo.Client
	mcp *mcp.Server
}

// New builds the MCP server and registers every tool.
func New(cfg Config, api *roomtodo.Client) *Server {
	s := &Server{cfg: cfg, api: api}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "roomtodo", Title: "Roomtodo planner", Version: Version},
		&mcp.ServerOptions{Instructions: instructions})
	s.registerTools()
	return s
}

// MCP returns the underlying MCP server (for the stdio transport).
func (s *Server) MCP() *mcp.Server { return s.mcp }

const instructions = `Tools for the roomtodo.com 3D room planner (unofficial).

Projects can live in three places, chosen by the "project" / "save_to" argument:
- a local .json file (works without signing in; the user opens it at roomtodo.com/planner with Ctrl+Alt+O),
- the user's roomtodo account (a numeric project id; "new" creates one; needs sign-in),
- a public share link (read-only; save a copy with save_to).

Coordinates are floor-plan centimetres: x grows to the right, y grows down the plan. get_project
returns every room's min/max/center, so place furniture inside those bounds. Typical flow:
create_floorplan → search_catalog → add_object (furniture on the floor; doors and windows snap into
the nearest wall) → get_project to check. Products whose "plan" is not "free" need a paid roomtodo plan.`

// HTTPHandler serves MCP over Streamable HTTP at /mcp (or /<secret>/mcp) and project files at
// /files/<name> (or /<secret>/files/<name>).
func (s *Server) HTTPHandler() http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, nil)
	files := http.StripPrefix("/files/", http.FileServer(http.Dir(s.cfg.ProjectsDir)))

	mux := http.NewServeMux()
	mux.Handle("/mcp", withBase(mcpHandler))
	mux.Handle("/files/", files)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("roomtodo-mcp " + Version + ": MCP endpoint is " + s.prefix() + "/mcp\n"))
	})
	if s.cfg.Secret == "" {
		return mux
	}
	prefix := "/" + s.cfg.Secret
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, prefix+"/") ||
			subtle.ConstantTimeCompare([]byte(r.URL.Path[:len(prefix)]), []byte(prefix)) != 1 {
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		r2.URL.RawPath = ""
		mux.ServeHTTP(w, r2)
	})
}

func (s *Server) prefix() string {
	if s.cfg.Secret == "" {
		return ""
	}
	return "/" + s.cfg.Secret
}

// baseHeader carries the request's public base URL from the HTTP layer to tool handlers.
const baseHeader = "X-Roomtodo-Mcp-Base"

func withBase(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Header.Get("X-Forwarded-Host")
		if host == "" {
			host = r.Host
		}
		scheme := r.Header.Get("X-Forwarded-Proto")
		if scheme == "" {
			scheme = "http"
			if r.TLS != nil {
				scheme = "https"
			}
		}
		r.Header.Set(baseHeader, scheme+"://"+host)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) downloadURL(h http.Header, path string) string {
	base := strings.TrimRight(s.cfg.PublicURL, "/")
	if base == "" && h != nil {
		base = h.Get(baseHeader)
	}
	if base == "" {
		return ""
	}
	return base + s.prefix() + "/files/" + url.PathEscape(filepath.Base(path))
}

func marshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", " ") }
