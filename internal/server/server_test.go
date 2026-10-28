package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Dobytchick/roomtodo-mcp/internal/roomtodo"
)

// TestOfflineFlow drives the server over MCP the way a model would, without signing in.
// It talks to the public roomtodo catalog, so it needs network access (skip with -short).
func TestOfflineFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("needs network")
	}
	dir := t.TempDir()
	srv := New(Config{ProjectsDir: dir}, roomtodo.New("", &roomtodo.TokenStore{Dir: t.TempDir()}))
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.MCP().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("%s failed: %s", name, text)
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(text), &out)
		return out
	}

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 15 {
		t.Fatalf("only %d tools", len(tools.Tools))
	}

	call("create_floorplan", map[string]any{
		"project": "house.json",
		"rooms": []any{
			map[string]any{"name": "Living", "x": 0, "y": 0, "width": 430, "depth": 660},
			map[string]any{"name": "Hall", "x": 430, "y": 0, "width": 160, "depth": 660},
			map[string]any{"name": "Bedroom", "x": 590, "y": 0, "width": 390, "depth": 660},
		},
		"openings": []any{
			map[string]any{"between": []any{"Hall", "Living"}},
			map[string]any{"between": []any{"Hall", "Bedroom"}},
		},
	})
	if _, err := os.Stat(dir + "/house.json"); err != nil {
		t.Fatal("project file not written")
	}

	found := call("search_catalog", map[string]any{"query": "диван", "free_only": true, "limit": 3})
	if len(found["items"].([]any)) == 0 {
		t.Fatal("catalog search found nothing")
	}

	sofa := call("add_object", map[string]any{"project": "house.json", "product_id": "951", "room": "Living"})
	door := call("add_object", map[string]any{"project": "house.json", "product_id": "941", "room": "Hall", "side": "bottom"})
	window := call("add_object", map[string]any{"project": "house.json", "product_id": "235", "room": "Bedroom", "side": "right"})

	if w := door["result"].(map[string]any)["wall"].(map[string]any); !strings.Contains(strings.Join(toStrings(w["between"]), ","), "outside") {
		t.Errorf("entrance door should be in an exterior wall, got %v", w["between"])
	}
	if r := window["result"].(map[string]any)["rotation_deg"].(float64); r != 90 && r != -90 {
		t.Errorf("window in a vertical wall should be rotated ±90°, got %v", r)
	}
	_ = sofa

	p := call("get_project", map[string]any{"project": "house.json"})
	objs := p["objects"].([]any)
	if len(objs) != 3 {
		t.Fatalf("want 3 objects, got %d", len(objs))
	}
	if objs[0].(map[string]any)["room"] != "Living" {
		t.Errorf("sofa should be in Living: %v", objs[0])
	}
	t.Logf("door: %v\nwindow: %v", door["result"], window["result"])
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
