// Package plan edits roomtodo project JSON ("plan") as produced by the planner's
// R2D.Scene.getSceneState. Plans are kept as generic maps so fields this package does not know
// about survive a load → edit → save round trip.
//
// Coordinates: the floor plan uses x/y in centimetres (construction points). Scene products use
// x/z on the floor with z = -y, and y as elevation above the floor.
package plan

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// Plan is a project as stored by roomtodo.
type Plan = map[string]any

// DefaultWallHeight is the planner's default wall height (cm).
const DefaultWallHeight = 280

// New returns an empty project, identical to the planner's "New project" state.
func New(name string) Plan {
	if name == "" {
		name = "New project"
	}
	return Plan{
		"id":        0,
		"name":      name,
		"extraData": nil,
		"dimension": 0,
		"construction": map[string]any{
			"points": []any{}, "walls": []any{}, "cuts": []any{}, "rooms": []any{}, "covers": []any{}, "areas": []any{},
			"cap": map[string]any{"m": "#606b67", "addM": "41803", "cv": true, "cvisible": true, "cpv": false,
				"mr": 0, "mx": 0, "my": 0, "wh": DefaultWallHeight},
			"rulers": []any{},
			"list":   []any{"41803"},
		},
		"scene": map[string]any{
			"products": []any{},
			"groups":   []any{},
			"viewState": map[string]any{"state": "2d",
				"camera": map[string]any{"px": 0, "py": 0, "pz": 0, "tilt": 90, "pan": 0, "zoom": 0.1}},
			"skybox":             map[string]any{"id": "", "r": 0, "tags": []any{}},
			"additionalSettings": map[string]any{"showPartialVisibleElements": false},
			"hiddenElements": map[string]any{"objects": []any{}, "walls": []any{}, "covers": []any{}, "groups": []any{},
				"bottomPlinths": []any{}, "topPlinths": []any{}, "ceilings": []any{}, "frames": []any{}, "cap": []any{},
				"areas": []any{}, "cuts": []any{}},
		},
		"viewOnly":  false,
		"floorplan": nil,
		"preview":   nil,
		"user_key":  nil,
		"hash":      0,
	}
}

// Parse decodes plan JSON.
func Parse(b []byte) (Plan, error) {
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if _, ok := p["construction"]; !ok {
		return nil, fmt.Errorf("not a roomtodo project: no \"construction\"")
	}
	return p, nil
}

// ---- generic map helpers ----

func obj(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	if v == nil {
		v = map[string]any{}
		m[key] = v
	}
	return v
}

func list(m map[string]any, key string) []any {
	v, _ := m[key].([]any)
	return v
}

func maps(v []any) []map[string]any {
	out := make([]map[string]any, 0, len(v))
	for _, x := range v {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// Construction returns the construction object (some API responses nest it one level deeper).
func Construction(p Plan) map[string]any {
	c := obj(p, "construction")
	if inner, ok := c["construction"].(map[string]any); ok {
		return inner
	}
	return c
}

// Scene returns the scene object.
func Scene(p Plan) map[string]any { return obj(p, "scene") }

// Products returns the placed products.
func Products(p Plan) []map[string]any { return maps(list(Scene(p), "products")) }

// WallsHeight returns the global wall height (cm).
func WallsHeight(p Plan) float64 {
	if h := num(obj(Construction(p), "cap")["wh"]); h > 0 {
		return h
	}
	return DefaultWallHeight
}

// SetWallsHeight sets the global wall height and the height of every room.
func SetWallsHeight(p Plan, h float64) {
	c := Construction(p)
	obj(c, "cap")["wh"] = h
	for _, a := range maps(list(c, "areas")) {
		a["h"] = h
		a["ch"] = h
	}
}

// addToList records ids the planner must preload (construction.list).
func addToList(p Plan, ids ...any) {
	c := obj(p, "construction") // list lives on the outer construction object
	seen := map[string]bool{}
	cur := list(c, "list")
	for _, v := range cur {
		seen[str(v)] = true
	}
	for _, id := range ids {
		s := str(id)
		if s == "" || s == "0" || s[0] == '#' || seen[s] {
			continue
		}
		seen[s] = true
		cur = append(cur, id)
	}
	c["list"] = cur
}
