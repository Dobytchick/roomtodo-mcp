package plan

import (
	"math"
	"sort"
)

// Pt is a floor-plan point (cm).
type Pt struct{ X, Y float64 }

// Segment is a wall face between two construction points.
type Segment struct {
	ID   string
	A, B Pt
}

// Room is a floor area ("areas" in the construction) with its outline.
type Room struct {
	ID      string
	Name    string
	Outline []Pt
	Walls   []string
	// CoverID is the room's floor ("covers"), AreaID its wall-bounded area ("areas"); either may be empty.
	CoverID, AreaID string
}

type geometry struct {
	points map[string]Pt
	walls  []Segment
	rooms  []Room
}

func readGeometry(p Plan) geometry {
	c := Construction(p)
	g := geometry{points: map[string]Pt{}}
	for _, pt := range maps(list(c, "points")) {
		g.points[str(pt["id"])] = Pt{num(pt["x"]), num(pt["y"])}
	}
	wallByID := map[string]Segment{}
	for _, w := range maps(list(c, "walls")) {
		a, okA := g.points[str(w["pa"])]
		b, okB := g.points[str(w["pb"])]
		if !okA || !okB {
			continue
		}
		s := Segment{ID: str(w["id"]), A: a, B: b}
		g.walls = append(g.walls, s)
		wallByID[s.ID] = s
	}
	cutByID := map[string][2]Pt{}
	for _, ct := range maps(list(c, "cuts")) {
		a, okA := g.points[str(ct["pa"])]
		b, okB := g.points[str(ct["pb"])]
		if okA && okB {
			cutByID[str(ct["id"])] = [2]Pt{a, b}
		}
	}
	for _, a := range maps(list(c, "areas")) {
		var pairs [][2]Pt
		var wallIDs []string
		for _, id := range list(a, "walls") {
			if s, ok := wallByID[str(id)]; ok {
				pairs = append(pairs, [2]Pt{s.A, s.B})
				wallIDs = append(wallIDs, s.ID)
			}
		}
		for _, id := range list(a, "cuts") {
			if s, ok := cutByID[str(id)]; ok {
				pairs = append(pairs, s)
			}
		}
		g.rooms = append(g.rooms, Room{ID: str(a["id"]), AreaID: str(a["id"]), Name: str(a["name"]), Outline: chain(pairs), Walls: wallIDs})
	}

	// Floors ("covers") are what the planner shows as rooms: they carry the floor material and the
	// room label, and survive re-saving in the planner (area names do not). Prefer them.
	var floors []Room
	for _, cv := range maps(list(c, "covers")) {
		if num(cv["o"]) != 1 {
			continue // a hole in a floor
		}
		var outline []Pt
		for _, id := range list(cv, "points") {
			if pt, ok := g.points[str(id)]; ok {
				outline = append(outline, pt)
			}
		}
		if len(outline) < 3 {
			continue
		}
		r := Room{ID: str(cv["id"]), CoverID: str(cv["id"]), Outline: outline}
		if t, ok := cv["title"].(map[string]any); ok {
			r.Name = str(t["text"])
		}
		in := interiorPoint(outline)
		for _, a := range g.rooms {
			if len(a.Outline) > 2 && inside(in, a.Outline) {
				r.AreaID, r.Walls = a.AreaID, a.Walls
				if r.Name == "" || r.Name == "Room" {
					r.Name = a.Name
				}
				break
			}
		}
		floors = append(floors, r)
	}
	if len(floors) > 0 {
		g.rooms = floors
	}
	return g
}

// interiorPoint returns a point strictly inside a simple polygon (the centroid, or a point just
// inside the first edge when the centroid falls outside, e.g. for L-shapes).
func interiorPoint(poly []Pt) Pt {
	c := centroid(poly)
	if inside(c, poly) {
		return c
	}
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		m := Pt{(a.X + b.X) / 2, (a.Y + b.Y) / 2}
		dx, dy := b.X-a.X, b.Y-a.Y
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		for _, s := range []float64{1, -1} {
			q := Pt{m.X - s*dy/l, m.Y + s*dx/l}
			if inside(q, poly) {
				return q
			}
		}
	}
	return c
}

// chain joins unordered segments into one closed outline (like WC.contourFromPairs).
func chain(pairs [][2]Pt) []Pt {
	if len(pairs) == 0 {
		return nil
	}
	used := make([]bool, len(pairs))
	out := []Pt{pairs[0][0], pairs[0][1]}
	used[0] = true
	for {
		end := out[len(out)-1]
		found := false
		for i, s := range pairs {
			if used[i] {
				continue
			}
			if near(s[0], end) {
				out = append(out, s[1])
			} else if near(s[1], end) {
				out = append(out, s[0])
			} else {
				continue
			}
			used[i], found = true, true
			break
		}
		if !found {
			break
		}
	}
	if len(out) > 1 && near(out[0], out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out
}

func near(a, b Pt) bool { return math.Abs(a.X-b.X) < 0.5 && math.Abs(a.Y-b.Y) < 0.5 }

func polyArea(pts []Pt) float64 {
	s := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		s += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
	}
	return s / 2
}

func centroid(pts []Pt) Pt {
	a := polyArea(pts)
	if math.Abs(a) < 1e-9 {
		var c Pt
		for _, p := range pts {
			c.X += p.X / float64(len(pts))
			c.Y += p.Y / float64(len(pts))
		}
		return c
	}
	var cx, cy float64
	for i := range pts {
		j := (i + 1) % len(pts)
		f := pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
		cx += (pts[i].X + pts[j].X) * f
		cy += (pts[i].Y + pts[j].Y) * f
	}
	return Pt{cx / (6 * a), cy / (6 * a)}
}

func bounds(pts []Pt) (min, max Pt) {
	min = Pt{math.Inf(1), math.Inf(1)}
	max = Pt{math.Inf(-1), math.Inf(-1)}
	for _, p := range pts {
		min.X, min.Y = math.Min(min.X, p.X), math.Min(min.Y, p.Y)
		max.X, max.Y = math.Max(max.X, p.X), math.Max(max.Y, p.Y)
	}
	return
}

func inside(p Pt, poly []Pt) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a.Y > p.Y) != (b.Y > p.Y) && p.X < (b.X-a.X)*(p.Y-a.Y)/(b.Y-a.Y)+a.X {
			in = !in
		}
	}
	return in
}

// project returns the closest point to p on segment s and the parameter t in [0,1].
func project(p Pt, s Segment) (Pt, float64) {
	dx, dy := s.B.X-s.A.X, s.B.Y-s.A.Y
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return s.A, 0
	}
	t := ((p.X-s.A.X)*dx + (p.Y-s.A.Y)*dy) / l2
	t = math.Max(0, math.Min(1, t))
	return Pt{s.A.X + t*dx, s.A.Y + t*dy}, t
}

func dist(a, b Pt) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

// WallSpot is a point on a wall's centre line, where doors and windows snap.
type WallSpot struct {
	Center    Pt
	Thickness float64
	// Angle of the wall direction in degrees (0 = along +x).
	Angle  float64
	WallID string
	// Rooms on either side (names; "outside" for the exterior).
	Sides [2]string
}

// FindWallSpot finds the wall centre line nearest to p: it takes the closest wall face, then
// the parallel face on the other side of the same wall body.
func FindWallSpot(p Plan, at Pt) (*WallSpot, error) {
	g := readGeometry(p)
	if len(g.walls) == 0 {
		return nil, errf("the project has no walls")
	}
	type cand struct {
		s Segment
		q Pt
		d float64
	}
	var cs []cand
	for _, s := range g.walls {
		q, _ := project(at, s)
		cs = append(cs, cand{s, q, dist(at, q)})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].d < cs[j].d })

	for _, c := range cs[:min(len(cs), 6)] {
		f := c.s
		fx, fy := f.B.X-f.A.X, f.B.Y-f.A.Y
		fl := math.Hypot(fx, fy)
		if fl == 0 {
			continue
		}
		nx, ny := -fy/fl, fx/fl // unit normal
		best := math.Inf(1)
		var spot *WallSpot
		for _, o := range g.walls {
			if o.ID == f.ID {
				continue
			}
			ox, oy := o.B.X-o.A.X, o.B.Y-o.A.Y
			ol := math.Hypot(ox, oy)
			if ol == 0 || math.Abs(fx*oy-fy*ox)/(fl*ol) > 0.01 { // not parallel
				continue
			}
			// signed distance from face f to the other face along the normal
			t := (o.A.X-c.q.X)*nx + (o.A.Y-c.q.Y)*ny
			if math.Abs(t) < 2 || math.Abs(t) > 100 || math.Abs(t) >= math.Abs(best) {
				continue
			}
			foot := Pt{c.q.X + nx*t, c.q.Y + ny*t}
			if _, u := project(foot, o); dist(projectPt(foot, o), foot) > 0.5 || u < 0 || u > 1 {
				continue // the faces do not overlap here
			}
			mid := Pt{c.q.X + nx*t/2, c.q.Y + ny*t/2}
			if g.roomAt(mid) != "" { // that's floor between two walls, not a wall body
				continue
			}
			best = t
			spot = &WallSpot{Center: mid, Thickness: math.Abs(t), WallID: f.ID,
				Angle: math.Atan2(fy, fx) * 180 / math.Pi}
			spot.Sides = [2]string{g.roomName(Pt{mid.X - nx*t, mid.Y - ny*t}), g.roomName(Pt{mid.X + nx*t, mid.Y + ny*t})}
		}
		if spot != nil {
			return spot, nil
		}
	}
	return nil, errf("no wall found near (%.0f, %.0f)", at.X, at.Y)
}

func projectPt(p Pt, s Segment) Pt { q, _ := project(p, s); return q }

func (g geometry) roomAt(p Pt) string {
	for _, r := range g.rooms {
		if len(r.Outline) > 2 && inside(p, r.Outline) {
			if r.Name != "" {
				return r.Name
			}
			return "room " + r.ID
		}
	}
	return ""
}

func (g geometry) roomName(p Pt) string {
	if n := g.roomAt(p); n != "" {
		return n
	}
	return "outside"
}

// RoomByName returns a room (floor area) by name or id.
func RoomByName(p Plan, name string) (*Room, error) {
	for _, r := range readGeometry(p).rooms {
		if r.Name == name || r.ID == name {
			r := r
			return &r, nil
		}
	}
	return nil, errf("no room named %q (see get_project for room names)", name)
}

// RoomCenter returns the centre of a room's floor.
func (r Room) Center() Pt { return centroid(r.Outline) }
