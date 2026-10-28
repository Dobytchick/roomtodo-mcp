package plan

import "math"

// RoomInfo describes a room for the model.
type RoomInfo struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	AreaM2   float64    `json:"area_m2"`
	Center   [2]float64 `json:"center"`
	Min      [2]float64 `json:"min"`
	Max      [2]float64 `json:"max"`
	Floor    string     `json:"floor_material,omitempty"`
	Ceiling  string     `json:"ceiling_material,omitempty"`
	HeightCM float64    `json:"height_cm,omitempty"`
}

// WallInfo is one wall face.
type WallInfo struct {
	ID       string     `json:"id"`
	From     [2]float64 `json:"from"`
	To       [2]float64 `json:"to"`
	LengthCM float64    `json:"length_cm"`
}

// ObjectInfo is a placed product.
type ObjectInfo struct {
	ObjID     string     `json:"objid"`
	ProductID string     `json:"product_id"`
	Position  [2]float64 `json:"position"`
	Elevation float64    `json:"elevation"`
	Rotation  float64    `json:"rotation_deg"`
	SizeCM    [3]float64 `json:"size_cm"`
	InWall    bool       `json:"in_wall,omitempty"`
	Room      string     `json:"room,omitempty"`
	Hidden    bool       `json:"hidden,omitempty"`
}

// Summary is a compact view of a project. Coordinates are floor-plan x/y in cm.
type Summary struct {
	ID          any            `json:"id"`
	Name        string         `json:"name"`
	Hash        any            `json:"hash,omitempty"`
	WallsHeight float64        `json:"walls_height_cm"`
	Rooms       []RoomInfo     `json:"rooms"`
	Objects     []ObjectInfo   `json:"objects"`
	Walls       []WallInfo     `json:"walls,omitempty"`
	Counts      map[string]int `json:"counts"`
}

// Summarize builds a Summary; withWalls includes every wall face.
func Summarize(p Plan, withWalls bool) Summary {
	g := readGeometry(p)
	c := Construction(p)
	s := Summary{ID: p["id"], Name: str(p["name"]), Hash: p["hash"], WallsHeight: WallsHeight(p),
		Rooms: []RoomInfo{}, Objects: []ObjectInfo{}}
	byID := func(key string) map[string]map[string]any {
		m := map[string]map[string]any{}
		for _, o := range maps(list(c, key)) {
			m[str(o["id"])] = o
		}
		return m
	}
	areas, covers := byID("areas"), byID("covers")
	for _, r := range g.rooms {
		if len(r.Outline) < 3 {
			continue
		}
		mn, mx := bounds(r.Outline)
		ce := interiorPoint(r.Outline)
		src := covers[r.CoverID] // the visible floor, else the area
		if src == nil {
			src = areas[r.AreaID]
		}
		info := RoomInfo{ID: r.ID, Name: r.Name, AreaM2: math.Round(math.Abs(polyArea(r.Outline))/100) / 100,
			Center: [2]float64{round1(ce.X), round1(ce.Y)}, Min: [2]float64{mn.X, mn.Y}, Max: [2]float64{mx.X, mx.Y},
			HeightCM: num(src["ch"])}
		info.Floor = finish(src["m"], src["addM"])
		info.Ceiling = finish(src["mc"], src["addMc"])
		s.Rooms = append(s.Rooms, info)
	}
	for _, pr := range Products(p) {
		pos := Pt{num(pr["x"]), -num(pr["z"])}
		o := ObjectInfo{ObjID: str(pr["objid"]), ProductID: str(pr["id"]),
			Position: [2]float64{round1(pos.X), round1(pos.Y)}, Elevation: num(pr["y"]), Rotation: num(pr["ry"]),
			SizeCM: [3]float64{num(pr["width"]), num(pr["height"]), num(pr["depth"])}}
		o.InWall, _ = pr["forWall"].(bool)
		if v, ok := pr["visible"].(bool); ok && !v {
			o.Hidden = true
		}
		o.Room = g.roomAt(pos)
		s.Objects = append(s.Objects, o)
	}
	if withWalls {
		for _, w := range g.walls {
			s.Walls = append(s.Walls, WallInfo{ID: w.ID, From: [2]float64{w.A.X, w.A.Y}, To: [2]float64{w.B.X, w.B.Y},
				LengthCM: math.Round(dist(w.A, w.B))})
		}
	}
	s.Counts = map[string]int{"rooms": len(s.Rooms), "walls": len(g.walls), "objects": len(s.Objects)}
	return s
}

// finish describes a surface: "2890" (material), "#dfe8ec on 41823" (colour on a colourable base).
func finish(m, addM any) string {
	if a := str(addM); a != "" {
		return str(m) + " on " + a
	}
	return str(m)
}
