// Package server exposes roomtodo as MCP tools.
package server

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Dobytchick/roomtodo-mcp/internal/roomtodo"
)

// Version is set at build time (-ldflags "-X .../server.Version=v1.2.3").
var Version = "dev"

// Config configures the server.
type Config struct {
	// ProjectsDir holds local project files (relative names resolve here).
	ProjectsDir string
	// BrowserLogin enables the interactive browser sign-in.
	BrowserLogin bool
}

// Server holds the shared state of all MCP sessions.
type Server struct {
	cfg Config
	api *roomtodo.Client
	mcp *mcp.Server
}

// New builds the MCP server and registers every tool and prompt.
func New(cfg Config, api *roomtodo.Client) *Server {
	s := &Server{cfg: cfg, api: api}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "roomtodo", Title: "Roomtodo planner", Version: Version},
		&mcp.ServerOptions{Instructions: instructions})
	s.registerTools()
	s.registerPrompts()
	return s
}

// MCP returns the underlying MCP server.
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

func marshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", " ") }
