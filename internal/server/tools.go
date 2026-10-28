package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Dobytchick/roomtodo-mcp/internal/floorplan"
	"github.com/Dobytchick/roomtodo-mcp/internal/plan"
	"github.com/Dobytchick/roomtodo-mcp/internal/roomtodo"
)

func ptr[T any](v T) *T { return &v }

var (
	readOnly    = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}
	edits       = &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}
	destructive = &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}
)

// tool registers a typed tool whose result is returned as JSON.
func tool[In any](s *Server, name, title, desc string, ann *mcp.ToolAnnotations, h func(context.Context, *mcp.CallToolRequest, In) (any, error)) {
	mcp.AddTool(s.mcp, &mcp.Tool{Name: name, Title: title, Description: desc, Annotations: ann},
		func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			out, err := h(ctx, req, in)
			return nil, out, err
		})
}

type projectArg struct {
	Project string `json:"project" jsonschema:"Which project: a .json file name (local, no sign-in needed), an account project id, or a public share link."`
	SaveTo  string `json:"save_to,omitempty" jsonschema:"Where to save the result instead of overwriting the source: a .json file name, an account project id, or \"new\" for a new account project."`
}

func (s *Server) registerTools() {
	// ---------- account ----------
	type loginIn struct {
		Token string `json:"token,omitempty" jsonschema:"roomtodo token to use instead of the browser sign-in (from localStorage r2d_token_* on roomtodo.com)"`
		Force bool   `json:"force,omitempty" jsonschema:"ignore the cached token and sign in again"`
	}
	tool(s, "login", "Sign in",
		"Sign in to roomtodo. Without a token it opens a browser window on roomtodo.com on the computer running this server and waits until the user signs in. Only needed for account projects; files and the catalog work without it.",
		edits, func(ctx context.Context, _ *mcp.CallToolRequest, in loginIn) (any, error) {
			if in.Token != "" {
				if err := s.api.SetToken(in.Token, true); err != nil {
					return nil, err
				}
			} else {
				if !s.cfg.BrowserLogin {
					return nil, errors.New("browser sign-in is disabled on this server: pass a token")
				}
				if _, err := s.api.EnsureToken(ctx, in.Force); err != nil {
					return nil, err
				}
			}
			return s.api.UserInfo(ctx)
		})

	tool(s, "logout", "Sign out", "Forget the cached roomtodo token.", destructive,
		func(context.Context, *mcp.CallToolRequest, struct{}) (any, error) {
			return "signed out", s.api.Logout()
		})

	tool(s, "whoami", "Current user", "The signed-in roomtodo user (signs in first if needed).", readOnly,
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (any, error) { return s.api.UserInfo(ctx) })

	// ---------- projects ----------
	type listIn struct {
		Limit  int `json:"limit,omitempty" jsonschema:"default 50"`
		Offset int `json:"offset,omitempty"`
	}
	tool(s, "list_projects", "List projects",
		"List the projects in the user's roomtodo account, and the local project files.", readOnly,
		func(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (any, error) {
			if in.Limit == 0 {
				in.Limit = 50
			}
			out := map[string]any{"files": s.localFiles()}
			if s.api.HasToken() || s.cfg.BrowserLogin {
				acc, err := s.api.ListProjects(ctx, in.Limit, in.Offset)
				if err != nil {
					out["account_error"] = err.Error()
				} else {
					out["account"] = acc["data"]
				}
			} else {
				out["account"] = "not signed in"
			}
			return out, nil
		})

	type newIn struct {
		Name   string `json:"name"`
		SaveTo string `json:"save_to" jsonschema:"a .json file name (no sign-in needed) or \"new\" for a new account project"`
	}
	tool(s, "new_project", "New project", "Create an empty project.", edits,
		func(ctx context.Context, req *mcp.CallToolRequest, in newIn) (any, error) {
			dst, err := s.parseRef(in.SaveTo)
			if err != nil {
				return nil, err
			}
			p := plan.New(in.Name)
			return s.save(ctx, dst, p)
		})

	type getIn struct {
		Project   string `json:"project" jsonschema:"Which project: a .json file name (local, no sign-in needed), an account project id, or a public share link."`
		WithWalls bool   `json:"with_walls,omitempty" jsonschema:"also list every wall face"`
	}
	tool(s, "get_project", "Show project",
		"Summary of a project: rooms (with min/max/center in cm), placed objects and, optionally, walls.", readOnly,
		func(ctx context.Context, _ *mcp.CallToolRequest, in getIn) (any, error) {
			r, err := s.parseRef(in.Project)
			if err != nil {
				return nil, err
			}
			p, err := s.load(ctx, r)
			if err != nil {
				return nil, err
			}
			return plan.Summarize(p, in.WithWalls), nil
		})

	type copyIn struct {
		Project string `json:"project" jsonschema:"Which project: a .json file name, an account project id, or a public share link."`
		SaveTo  string `json:"save_to" jsonschema:"a .json file name, an account project id to overwrite, or \"new\" for a new account project"`
		Name    string `json:"name,omitempty" jsonschema:"new project name"`
	}
	tool(s, "save_project_as", "Save project as",
		"Copy a project between places: download an account or shared project to a file, upload a file to the account, or duplicate.", edits,
		func(ctx context.Context, req *mcp.CallToolRequest, in copyIn) (any, error) {
			return s.edit(ctx, in.Project, in.SaveTo, func(p plan.Plan) (any, error) {
				if in.Name != "" {
					p["name"] = in.Name
				}
				return nil, nil
			})
		})

	type renameIn struct {
		ID      string `json:"id" jsonschema:"account project id"`
		Name    string `json:"name"`
		OldName string `json:"old_name,omitempty"`
	}
	tool(s, "rename_project", "Rename project", "Rename an account project.", edits,
		func(ctx context.Context, _ *mcp.CallToolRequest, in renameIn) (any, error) {
			return s.api.RenameProject(ctx, in.ID, in.Name, in.OldName)
		})

	type deleteIn struct {
		ID      string `json:"id" jsonschema:"account project id"`
		Confirm bool   `json:"confirm" jsonschema:"must be true: deleting is permanent"`
	}
	tool(s, "delete_project", "Delete project", "Delete an account project permanently. Requires confirm: true.", destructive,
		func(ctx context.Context, _ *mcp.CallToolRequest, in deleteIn) (any, error) {
			if !in.Confirm {
				return nil, errors.New("set confirm: true to delete")
			}
			return s.api.DeleteProject(ctx, in.ID)
		})

	type idIn struct {
		ID string `json:"id" jsonschema:"account project id"`
	}
	tool(s, "share_project", "Share project", "Make an account project public and return its link.", edits,
		func(ctx context.Context, _ *mcp.CallToolRequest, in idIn) (any, error) {
			r, err := s.api.ShareLink(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			for _, k := range []string{"hash", "plan_hash"} {
				if h, ok := r[k].(string); ok && h != "" {
					r["url"] = "https://roomtodo.com/planner/project/" + h
				}
			}
			return r, nil
		})

	// ---------- floor plan ----------
	type floorIn struct {
		projectArg
		floorplan.Spec
	}
	tool(s, "create_floorplan", "Create floor plan",
		`Build walls and rooms from rectangles. Rooms are touching rectangles in cm (x right, y down); a partition is centred on each shared border and exterior walls are added outside. "openings" are doorways between rooms that share a wall. Replaces the project's existing walls and rooms; placed objects are kept. Doors and windows are then added with add_object.`,
		edits, func(ctx context.Context, req *mcp.CallToolRequest, in floorIn) (any, error) {
			return s.edit(ctx, in.Project, in.SaveTo, func(p plan.Plan) (any, error) {
				return floorplan.Build(p, in.Spec)
			})
		})

	type heightIn struct {
		projectArg
		HeightCM float64 `json:"height_cm" jsonschema:"wall height in cm"`
	}
	tool(s, "set_walls_height", "Set wall height", "Set the wall and ceiling height of the whole project.", edits,
		func(ctx context.Context, req *mcp.CallToolRequest, in heightIn) (any, error) {
			if in.HeightCM < 100 || in.HeightCM > 1000 {
				return nil, errors.New("height_cm must be between 100 and 1000")
			}
			return s.edit(ctx, in.Project, in.SaveTo, func(p plan.Plan) (any, error) {
				plan.SetWallsHeight(p, in.HeightCM)
				return nil, nil
			})
		})

	type materialIn struct {
		projectArg
		Target   string `json:"target" jsonschema:"floor, ceiling or walls"`
		Room     string `json:"room,omitempty" jsonschema:"room name; empty means every room"`
		Material string `json:"material,omitempty" jsonschema:"catalog material id (search_catalog kind=material); colourable ones (colorable: true) take color too"`
		Color    string `json:"color,omitempty" jsonschema:"colour like #e0d5c0: alone it paints the surface, with a colourable material it tints it"`
	}
	tool(s, "set_material", "Set material",
		"Apply a catalog material, a colour, or both (for colourable materials such as matte tile) to the floor, ceiling or walls of a room.", edits,
		func(ctx context.Context, req *mcp.CallToolRequest, in materialIn) (any, error) {
			var f plan.Finish
			switch {
			case in.Material == "" && in.Color == "":
				return nil, errors.New("give material, color, or both")
			case in.Material == "":
				f = plan.ColorFinish(in.Color, "")
			case strings.HasPrefix(in.Material, "#"): // a colour passed as material
				f = plan.ColorFinish(in.Material, "")
			default:
				product, err := s.api.Product(ctx, in.Material)
				if err != nil {
					return nil, err
				}
				if toF(product["type"]) != 1 {
					return nil, fmt.Errorf("%s is an object, not a material: use add_object", in.Material)
				}
				if b, _ := product["isColorPicker"].(bool); b {
					color := in.Color
					if color == "" {
						color = "#ffffff"
					}
					f = plan.ColorFinish(color, in.Material)
				} else {
					f = plan.Finish{M: in.Material}
				}
			}
			return s.edit(ctx, in.Project, in.SaveTo, func(p plan.Plan) (any, error) {
				n, err := plan.SetMaterial(p, in.Target, in.Room, f)
				return map[string]any{"changed": n, "m": f.M, "addM": f.AddM}, err
			})
		})

	// ---------- catalog ----------
	type searchIn struct {
		Query    string   `json:"query,omitempty" jsonschema:"search words, e.g. sofa, диван, window, oak"`
		IDs      []string `json:"ids,omitempty" jsonschema:"fetch these product ids"`
		Kind     string   `json:"kind,omitempty" jsonschema:"object (furniture, doors, windows), material, or empty for both"`
		Category string   `json:"category_tag,omitempty" jsonschema:"catalog category tag, e.g. plinths"`
		FreeOnly bool     `json:"free_only,omitempty" jsonschema:"only items available on the free roomtodo plan"`
		Limit    int      `json:"limit,omitempty" jsonschema:"max results, default 20"`
		Offset   int      `json:"offset,omitempty"`
	}
	tool(s, "search_catalog", "Search catalog",
		`Search the roomtodo catalog (no sign-in needed). "placement" tells where an object goes: floor (furniture) or wall (doors, windows). Use the id with add_object or set_material.`,
		readOnly, func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (any, error) {
			q := roomtodo.CatalogQuery{Query: in.Query, IDs: in.IDs, CategoryTag: in.Category}
			switch in.Kind {
			case "material":
				q.CategoryType = 1
			case "object":
				q.CategoryType = 2
			}
			limit := in.Limit
			if limit == 0 {
				limit = 20
			}
			items, total, err := s.api.CatalogSearch(ctx, q)
			if err != nil {
				return nil, err
			}
			var out []map[string]any
			for _, it := range items {
				c := compactProduct(it)
				if in.FreeOnly && c["plan"] != "free" {
					continue
				}
				if (in.Kind == "material" && c["kind"] != "material") || (in.Kind == "object" && c["kind"] == "material") {
					continue
				}
				out = append(out, c)
			}
			if in.Offset < len(out) {
				out = out[in.Offset:]
			} else {
				out = nil
			}
			if len(out) > limit {
				out = out[:limit]
			}
			return map[string]any{"total": total, "items": out}, nil
		})

	tool(s, "category_tree", "Catalog categories", "The catalog category tree.", readOnly,
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (any, error) {
			return s.api.CategoryTree(ctx)
		})

	// ---------- objects ----------
	type addIn struct {
		projectArg
		ProductID string   `json:"product_id" jsonschema:"catalog id from search_catalog"`
		X         *float64 `json:"x,omitempty" jsonschema:"floor-plan x, cm"`
		Y         *float64 `json:"y,omitempty" jsonschema:"floor-plan y, cm"`
		Room      string   `json:"room,omitempty" jsonschema:"room name: without x/y furniture goes to the room centre"`
		Side      string   `json:"side,omitempty" jsonschema:"for doors/windows with room: which wall of the room (top, bottom, left, right)"`
		Offset    *float64 `json:"offset,omitempty" jsonschema:"for doors/windows with side: distance from the wall's left/top end of the room to the item centre, cm (default: middle)"`
		Rotation  float64  `json:"rotation,omitempty" jsonschema:"degrees around the vertical axis; for doors/windows 180 flips the side they face"`
		Elevation *float64 `json:"elevation,omitempty" jsonschema:"height of the bottom above the floor, cm (windows default to the catalog value)"`
		Scale     float64  `json:"scale,omitempty" jsonschema:"uniform size multiplier, default 1"`
	}
	tool(s, "add_object", "Add object",
		`Place a catalog object. Furniture goes on the floor at x/y (or the centre of "room"). Doors and windows snap into the nearest wall: give x/y near the wall, or "room" + "side" (+ "offset").`,
		edits, func(ctx context.Context, req *mcp.CallToolRequest, in addIn) (any, error) {
			product, err := s.api.Product(ctx, in.ProductID)
			if err != nil {
				return nil, err
			}
			return s.edit(ctx, in.Project, in.SaveTo, func(p plan.Plan) (any, error) {
				pl := plan.Placement{Rotation: in.Rotation, ScaleX: in.Scale, ScaleY: in.Scale, ScaleZ: in.Scale}
				switch {
				case in.X != nil && in.Y != nil:
					pl.X, pl.Y = *in.X, *in.Y
				case in.Room != "":
					r, err := plan.RoomByName(p, in.Room)
					if err != nil {
						return nil, err
					}
					c := r.Center()
					pl.X, pl.Y = c.X, c.Y
					if in.Side != "" {
						if pl.X, pl.Y, err = sidePoint(p, in.Room, in.Side, in.Offset); err != nil {
							return nil, err
						}
					}
				default:
					return nil, errors.New("give x and y, or a room")
				}
				if in.Elevation != nil {
					pl.Elevation = *in.Elevation
				} else {
					pl.Elevation = defaultElevation(product)
				}
				item, spot, err := plan.AddProduct(p, product, pl)
				if err != nil {
					return nil, err
				}
				res := map[string]any{"objid": item["objid"], "name": product["name"],
					"position": []any{item["x"], -toF(item["z"])}, "rotation_deg": item["ry"]}
				if spot != nil {
					res["wall"] = map[string]any{"between": spot.Sides, "thickness_cm": spot.Thickness}
				}
				return res, nil
			})
		})

	type moveIn struct {
		projectArg
		ObjID     string   `json:"objid" jsonschema:"objid from get_project"`
		X         *float64 `json:"x,omitempty"`
		Y         *float64 `json:"y,omitempty"`
		Rotation  *float64 `json:"rotation,omitempty" jsonschema:"degrees"`
		Elevation *float64 `json:"elevation,omitempty" jsonschema:"cm above the floor"`
		ScaleX    *float64 `json:"scale_x,omitempty"`
		ScaleY    *float64 `json:"scale_y,omitempty"`
		ScaleZ    *float64 `json:"scale_z,omitempty"`
		Visible   *bool    `json:"visible,omitempty"`
	}
	tool(s, "move_object", "Move object",
		"Move, rotate, lift, scale or hide a placed object. Doors and windows stay snapped to a wall.", edits,
		func(ctx context.Context, req *mcp.CallToolRequest, in moveIn) (any, error) {
			return s.edit(ctx, in.Project, in.SaveTo, func(p plan.Plan) (any, error) {
				_, err := plan.UpdateProduct(p, in.ObjID, plan.Patch{X: in.X, Y: in.Y, Rotation: in.Rotation, Elevation: in.Elevation,
					ScaleX: in.ScaleX, ScaleY: in.ScaleY, ScaleZ: in.ScaleZ, Visible: in.Visible})
				return nil, err
			})
		})

	type removeIn struct {
		projectArg
		ObjID string `json:"objid" jsonschema:"objid from get_project"`
	}
	tool(s, "remove_object", "Remove object", "Remove a placed object.", destructive,
		func(ctx context.Context, req *mcp.CallToolRequest, in removeIn) (any, error) {
			return s.edit(ctx, in.Project, in.SaveTo, func(p plan.Plan) (any, error) {
				_, err := plan.RemoveProduct(p, in.ObjID)
				return nil, err
			})
		})

	// ---------- renders ----------
	tool(s, "list_renders", "List renders", "The user's photo-realistic renders.", readOnly,
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (any, error) {
			return s.api.ListRenders(ctx)
		})
	tool(s, "get_render", "Get render", "One render by id.", readOnly,
		func(ctx context.Context, _ *mcp.CallToolRequest, in idIn) (any, error) {
			return s.api.GetRender(ctx, in.ID)
		})
}

func toF(v any) float64 {
	f, _ := v.(float64)
	return f
}

func (s *Server) localFiles() []string {
	var out []string
	entries, _ := os.ReadDir(s.cfg.ProjectsDir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	for i, n := range out {
		out[i] = filepath.Join(s.cfg.ProjectsDir, n)
	}
	return out
}

// sidePoint returns a point just outside the given side of a room, for wall snapping.
func sidePoint(p plan.Plan, room, side string, offset *float64) (float64, float64, error) {
	r, err := plan.RoomByName(p, room)
	if err != nil {
		return 0, 0, err
	}
	s := plan.Summarize(p, false)
	var info *plan.RoomInfo
	for i := range s.Rooms {
		if s.Rooms[i].ID == r.ID {
			info = &s.Rooms[i]
		}
	}
	if info == nil {
		return 0, 0, fmt.Errorf("room %q has no floor outline", room)
	}
	mn, mx := info.Min, info.Max
	along := func(lo, hi float64) float64 {
		if offset != nil {
			return lo + *offset
		}
		return (lo + hi) / 2
	}
	const out = 3 // just inside the wall body, so the nearest face is this room's wall
	switch side {
	case "top":
		return along(mn[0], mx[0]), mn[1] - out, nil
	case "bottom":
		return along(mn[0], mx[0]), mx[1] + out, nil
	case "left":
		return mn[0] - out, along(mn[1], mx[1]), nil
	case "right":
		return mx[0] + out, along(mn[1], mx[1]), nil
	}
	return 0, 0, errors.New("side must be top, bottom, left or right")
}

func defaultElevation(product map[string]any) float64 {
	prop, _ := product["property"].(map[string]any)
	pos, _ := prop["position"].(map[string]any)
	var f float64
	fmt.Sscan(fmt.Sprint(pos["y"]), &f)
	return f
}

// compactProduct keeps what a model needs to choose a product.
func compactProduct(it map[string]any) map[string]any {
	out := map[string]any{"id": it["id"], "name": it["name"]}
	prop, _ := it["property"].(map[string]any)
	if toF(it["type"]) == 1 {
		out["kind"] = "material"
		if b, _ := it["isColorPicker"].(bool); b {
			out["colorable"] = true
		}
	} else {
		out["kind"] = "object"
		out["placement"] = "floor"
		if plan.IsWallItem(it) {
			out["placement"] = "wall"
		}
	}
	if sizes, ok := prop["sizes"].(map[string]any); ok {
		out["size_cm"] = sizes
	}
	switch v := it["plan"].(type) {
	case string:
		out["plan"] = v
	case []any:
		var ps []string
		for _, x := range v {
			ps = append(ps, fmt.Sprint(x))
		}
		out["plan"] = strings.Join(ps, "/")
	}
	if src, ok := it["source"].(map[string]any); ok {
		if imgs, ok := src["images"].(map[string]any); ok {
			if pv, ok := imgs["preview"].(string); ok && pv != "" {
				out["preview"] = roomtodo.DefaultDomain + pv
			}
		}
	}
	var cats []string
	for _, key := range []string{"categoryProduct", "categoryMaterial"} {
		if m, ok := it[key].(map[string]any); ok {
			for _, v := range m {
				if arr, ok := v.([]any); ok && len(arr) > 0 {
					if c, ok := arr[0].(map[string]any); ok {
						cats = append(cats, fmt.Sprint(c["name"]))
					}
				}
			}
		}
	}
	if len(cats) > 0 {
		sort.Strings(cats)
		out["category"] = strings.Join(cats, ", ")
	}
	return out
}
