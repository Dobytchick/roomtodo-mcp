package plan

import (
	"fmt"
	"math"
	"strconv"
)

func errf(format string, a ...any) error { return fmt.Errorf(format, a...) }

// defaultFrameMaterial is what the planner assigns to door/window frames in walls.
const defaultFrameMaterial = "2013"

// Placement positions a product. X/Y are floor-plan coordinates (cm), Rotation is in degrees
// around the vertical axis, Elevation is the height of the product's bottom above the floor.
type Placement struct {
	X, Y      float64
	Elevation float64
	Rotation  float64
	// Scale multiplies the catalog size (0 means 1).
	ScaleX, ScaleY, ScaleZ float64
}

// IsWallItem reports whether a catalog product (door, window, ...) goes into a wall.
func IsWallItem(product map[string]any) bool {
	prop, _ := product["property"].(map[string]any)
	return prop != nil && str(prop["appointment"]) == "wall"
}

func productSizes(product map[string]any) (w, h, d float64) {
	prop, _ := product["property"].(map[string]any)
	sizes, _ := prop["sizes"].(map[string]any)
	return num(sizes["width"]), num(sizes["height"]), num(sizes["depth"])
}

func productMaterials(product map[string]any) []any {
	src, _ := product["source"].(map[string]any)
	body, _ := src["body"].(map[string]any)
	var out []any
	for _, m := range maps(list(body, "materials")) {
		cur := m["current"]
		if cur == nil || cur == "" {
			cur = m["default"]
		}
		out = append(out, map[string]any{
			"current": cur, "default": m["default"], "hash": m["hash"], "name": m["name"],
			"source": m["source"], "addMaterial": "",
		})
	}
	return out
}

func nextObjID(p Plan) string {
	max := 0.0
	for _, pr := range Products(p) {
		if n := num(pr["objid"]); n > max {
			max = n
		}
	}
	return strconv.Itoa(int(max) + 1)
}

func orOne(f float64) float64 {
	if f == 0 {
		return 1
	}
	return f
}

// AddProduct places a catalog product. Wall items (doors, windows) snap to the nearest wall
// centre line and take its direction; their depth is stretched to the wall thickness, as the
// planner does. It returns the stored product and, for wall items, where it snapped.
func AddProduct(p Plan, product map[string]any, pl Placement) (map[string]any, *WallSpot, error) {
	typ := int(num(product["type"]))
	if typ == 1 {
		return nil, nil, errf("product %v is a material, not an object: use set_material", product["id"])
	}
	if typ == 0 {
		typ = 2
	}
	w, h, d := productSizes(product)
	sx, sy, sz := orOne(pl.ScaleX), orOne(pl.ScaleY), orOne(pl.ScaleZ)
	item := map[string]any{
		"id": product["id"], "objid": nextObjID(p), "type": typ,
		"x": round1(pl.X), "y": round1(pl.Elevation), "z": round1(-pl.Y),
		"sx": sx, "sy": sy, "sz": sz,
		"rx": 0, "ry": pl.Rotation, "rz": 0,
		"width": round1(w * sx), "height": round1(h * sy), "depth": round1(d * sz),
		"isLockedOnScene": false, "visible": true, "partialVisible": false,
		"plan": product["plan"], "userKey": product["userKey"], "isOwner": false,
		"externalData": map[string]any{}, "isParametric": false,
		"fx": 0, "fy": 0, "fz": 0,
		"materials": productMaterials(product),
	}

	var spot *WallSpot
	if IsWallItem(product) {
		var err error
		spot, err = FindWallSpot(p, Pt{pl.X, pl.Y})
		if err != nil {
			return nil, nil, err
		}
		// Project the requested point onto the wall centre line.
		a := spot.Angle * math.Pi / 180
		ux, uy := math.Cos(a), math.Sin(a)
		along := (pl.X-spot.Center.X)*ux + (pl.Y-spot.Center.Y)*uy
		c := Pt{spot.Center.X + ux*along, spot.Center.Y + uy*along}
		spot.Center = Pt{round1(c.X), round1(c.Y)}
		item["x"], item["z"] = spot.Center.X, -spot.Center.Y
		// Scene rotation is about the vertical axis with z = -y, so a wall along +x is 0°.
		item["ry"] = normDeg(spot.Angle + pl.Rotation)
		if d > 0 {
			item["sz"] = (spot.Thickness + 1) / d
			item["depth"] = spot.Thickness + 1
		}
		item["forWall"] = true
		for k, add := range map[string]string{"mf": "addMf", "mb": "addMb"} {
			item[k], item[add] = defaultFrameMaterial, ""
			item[k+"r"], item[k+"x"], item[k+"y"] = 0, 0, 0
		}
	}

	sc := Scene(p)
	sc["products"] = append(list(sc, "products"), item)
	ids := []any{product["id"]}
	for _, m := range maps(list(item, "materials")) {
		ids = append(ids, m["current"])
	}
	if IsWallItem(product) {
		ids = append(ids, defaultFrameMaterial)
	}
	addToList(p, ids...)
	return item, spot, nil
}

func normDeg(a float64) float64 {
	a = math.Mod(a, 360)
	if a > 180 {
		a -= 360
	} else if a <= -180 {
		a += 360
	}
	return round1(a)
}

// FindProduct returns a placed product by objid.
func FindProduct(p Plan, objid string) (map[string]any, error) {
	for _, pr := range Products(p) {
		if str(pr["objid"]) == objid {
			return pr, nil
		}
	}
	return nil, errf("no placed object with objid %s (see get_project)", objid)
}

// Patch changes a placed product; nil fields are left as they are.
type Patch struct {
	X, Y, Elevation, Rotation *float64
	ScaleX, ScaleY, ScaleZ    *float64
	Visible                   *bool
}

// UpdateProduct moves/rotates/scales a placed product. Wall items are re-snapped to a wall.
func UpdateProduct(p Plan, objid string, pt Patch) (map[string]any, error) {
	pr, err := FindProduct(p, objid)
	if err != nil {
		return nil, err
	}
	x, y := num(pr["x"]), -num(pr["z"])
	if pt.X != nil {
		x = *pt.X
	}
	if pt.Y != nil {
		y = *pt.Y
	}
	if b, _ := pr["forWall"].(bool); b && (pt.X != nil || pt.Y != nil) {
		spot, err := FindWallSpot(p, Pt{x, y})
		if err != nil {
			return nil, err
		}
		a := spot.Angle * math.Pi / 180
		ux, uy := math.Cos(a), math.Sin(a)
		along := (x-spot.Center.X)*ux + (y-spot.Center.Y)*uy
		x, y = round1(spot.Center.X+ux*along), round1(spot.Center.Y+uy*along)
		pr["ry"] = normDeg(spot.Angle)
	}
	pr["x"], pr["z"] = round1(x), round1(-y)
	if pt.Elevation != nil {
		pr["y"] = *pt.Elevation
	}
	if pt.Rotation != nil {
		pr["ry"] = normDeg(*pt.Rotation)
	}
	scale := func(key, sizeKey string, v *float64) {
		if v == nil {
			return
		}
		old := orOne(num(pr[key]))
		pr[sizeKey] = round1(num(pr[sizeKey]) / old * *v)
		pr[key] = *v
	}
	scale("sx", "width", pt.ScaleX)
	scale("sy", "height", pt.ScaleY)
	scale("sz", "depth", pt.ScaleZ)
	if pt.Visible != nil {
		pr["visible"] = *pt.Visible
	}
	return pr, nil
}

// RemoveProduct deletes a placed product (and drops it from groups).
func RemoveProduct(p Plan, objid string) (map[string]any, error) {
	sc := Scene(p)
	prods := list(sc, "products")
	for i, v := range prods {
		m, _ := v.(map[string]any)
		if m == nil || str(m["objid"]) != objid {
			continue
		}
		sc["products"] = append(prods[:i:i], prods[i+1:]...)
		for _, g := range maps(list(sc, "groups")) {
			var keep []any
			for _, o := range list(g, "objects") {
				if str(o) != objid {
					keep = append(keep, o)
				}
			}
			g["objects"] = keep
		}
		return m, nil
	}
	return nil, errf("no placed object with objid %s (see get_project)", objid)
}

// SetMaterial applies a catalog material (id) or a colour ("#rrggbb") to the floor or ceiling
// of a room, or to the walls of a room (room "" means every room).
func SetMaterial(p Plan, target, room, material string) (int, error) {
	c := Construction(p)
	g := readGeometry(p)
	var rooms []Room
	for _, r := range g.rooms {
		if room == "" || r.Name == room || r.ID == room {
			rooms = append(rooms, r)
		}
	}
	if len(rooms) == 0 {
		return 0, errf("no room named %q", room)
	}
	field, add := "m", "addM"
	if target == "ceiling" {
		field, add = "mc", "addMc"
	}
	n := 0
	switch target {
	case "floor", "ceiling":
		for _, a := range maps(list(c, "areas")) {
			for _, r := range rooms {
				if str(a["id"]) == r.ID {
					a[field], a[add] = material, ""
					n++
				}
			}
		}
	case "walls":
		ids := map[string]bool{}
		for _, r := range rooms {
			for _, w := range r.Walls {
				ids[w] = true
			}
		}
		for _, w := range maps(list(c, "walls")) {
			if ids[str(w["id"])] {
				w["m"], w["addM"] = material, ""
				n++
			}
		}
	default:
		return 0, errf("target must be floor, ceiling or walls")
	}
	addToList(p, material)
	return n, nil
}
