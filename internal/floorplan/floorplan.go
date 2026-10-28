// Package floorplan turns a simple description (rooms as touching rectangles plus doorways)
// into roomtodo construction data: wall contours, floor areas and the cuts between them.
//
// Model: rooms are axis-aligned rectangles that may share borders. A partition wall is centred
// on every shared border (each room loses half the thickness there); exterior walls are built
// outside the rooms, so a room's exterior sides keep their full size. A doorway opens a gap in a
// partition; doors and windows themselves are products placed into walls afterwards.
//
// The geometry is computed exactly on a compressed grid made of every relevant coordinate.
package floorplan

import (
	"fmt"
	"math"
	"sort"

	"github.com/Dobytchick/roomtodo-mcp/internal/plan"
)

// Room is a rectangle in floor-plan coordinates (cm): x grows right, y grows "down" the plan.
type Room struct {
	Name   string  `json:"name" jsonschema:"room name shown in the planner, e.g. Kitchen"`
	X      float64 `json:"x" jsonschema:"left edge, cm"`
	Y      float64 `json:"y" jsonschema:"top edge, cm"`
	Width  float64 `json:"width" jsonschema:"size along x, cm"`
	Depth  float64 `json:"depth" jsonschema:"size along y, cm"`
	Height float64 `json:"height,omitempty" jsonschema:"ceiling height, cm (default: wall height)"`
}

// Opening is a doorway between two rooms that share a border.
type Opening struct {
	Between [2]string `json:"between" jsonschema:"names of the two rooms"`
	Width   float64   `json:"width,omitempty" jsonschema:"opening width, cm (default 90; use a large value such as 10000 for a fully open border)"`
	Offset  *float64  `json:"offset,omitempty" jsonschema:"distance from the start (smaller x or y) of the shared border to the opening, cm (default: centred)"`
}

// Spec describes a floor plan.
type Spec struct {
	Rooms         []Room    `json:"rooms"`
	Openings      []Opening `json:"openings,omitempty"`
	WallThickness float64   `json:"wall_thickness,omitempty" jsonschema:"cm, default 20"`
	WallHeight    float64   `json:"wall_height,omitempty" jsonschema:"cm, default 280"`
}

// Result reports what was built.
type Result struct {
	Rooms []RoomOut `json:"rooms"`
	Walls int       `json:"walls"`
	Cuts  int       `json:"cuts"`
	Notes []string  `json:"notes,omitempty"`
}

// RoomOut is the clear (inside) size of a built room.
type RoomOut struct {
	Name   string     `json:"name"`
	Min    [2]float64 `json:"min"`
	Max    [2]float64 `json:"max"`
	Center [2]float64 `json:"center"`
}

type rect struct{ x0, y0, x1, y1 float64 }

func (r rect) contains(x, y float64) bool { return x > r.x0 && x < r.x1 && y > r.y0 && y < r.y1 }
func (r rect) grow(d float64) rect        { return rect{r.x0 - d, r.y0 - d, r.x1 + d, r.y1 + d} }

// Build writes the floor plan into p, replacing its walls and rooms (placed objects are kept).
func Build(p plan.Plan, s Spec) (*Result, error) {
	t := s.WallThickness
	if t == 0 {
		t = 20
	}
	if t < 4 || t > 100 {
		return nil, fmt.Errorf("wall_thickness must be between 4 and 100 cm")
	}
	wh := s.WallHeight
	if wh == 0 {
		wh = plan.DefaultWallHeight
	}
	h := t / 2
	if len(s.Rooms) == 0 {
		return nil, fmt.Errorf("at least one room is required")
	}

	rooms := make([]rect, len(s.Rooms))
	byName := map[string]int{}
	for i, r := range s.Rooms {
		if r.Name == "" {
			return nil, fmt.Errorf("room %d has no name", i+1)
		}
		if _, dup := byName[r.Name]; dup {
			return nil, fmt.Errorf("duplicate room name %q", r.Name)
		}
		if r.Width < t*2 || r.Depth < t*2 {
			return nil, fmt.Errorf("room %q is too small", r.Name)
		}
		byName[r.Name] = i
		rooms[i] = rect{r.X, r.Y, r.X + r.Width, r.Y + r.Depth}
	}
	for i := range rooms {
		for j := i + 1; j < len(rooms); j++ {
			a, b := rooms[i], rooms[j]
			if math.Min(a.x1, b.x1)-math.Max(a.x0, b.x0) > 0.01 && math.Min(a.y1, b.y1)-math.Max(a.y0, b.y0) > 0.01 {
				return nil, fmt.Errorf("rooms %q and %q overlap", s.Rooms[i].Name, s.Rooms[j].Name)
			}
		}
	}

	// Doorway bands: the strip of partition removed by each opening, owned by the first room.
	type band struct {
		r     rect
		owner int
	}
	var bands []band
	for _, o := range s.Openings {
		ai, okA := byName[o.Between[0]]
		bi, okB := byName[o.Between[1]]
		if !okA || !okB {
			return nil, fmt.Errorf("opening between %q and %q: unknown room", o.Between[0], o.Between[1])
		}
		a, b := rooms[ai], rooms[bi]
		var vertical bool // border is a vertical line x = at
		var at, lo, hi float64
		switch {
		case eq(a.x1, b.x0) || eq(b.x1, a.x0):
			vertical, at = true, a.x1
			if eq(b.x1, a.x0) {
				at = a.x0
			}
			lo, hi = math.Max(a.y0, b.y0), math.Min(a.y1, b.y1)
		case eq(a.y1, b.y0) || eq(b.y1, a.y0):
			at = a.y1
			if eq(b.y1, a.y0) {
				at = a.y0
			}
			lo, hi = math.Max(a.x0, b.x0), math.Min(a.x1, b.x1)
		}
		if hi-lo <= t {
			return nil, fmt.Errorf("rooms %q and %q do not share a wall long enough for an opening", o.Between[0], o.Between[1])
		}
		w := o.Width
		if w == 0 {
			w = 90
		}
		w = math.Min(w, hi-lo-t)
		off := (hi - lo - w) / 2
		if o.Offset != nil {
			off = *o.Offset
		}
		if off < h || off+w > hi-lo-h {
			return nil, fmt.Errorf("opening between %q and %q does not fit: shared wall is %.0f cm, keep the opening %.0f cm away from its ends", o.Between[0], o.Between[1], hi-lo, h)
		}
		s0, s1 := lo+off, lo+off+w
		r := rect{at - h, s0, at + h, s1}
		if !vertical {
			r = rect{s0, at - h, s1, at + h}
		}
		bands = append(bands, band{r, ai})
	}

	// Compressed grid.
	var xs, ys []float64
	addR := func(r rect) { xs = append(xs, r.x0, r.x1); ys = append(ys, r.y0, r.y1) }
	for _, r := range rooms {
		addR(r)
		addR(r.grow(h))
		addR(r.grow(-h))
		addR(r.grow(t))
	}
	for _, b := range bands {
		addR(b.r)
	}
	xs, ys = uniq(xs), uniq(ys)
	nx, ny := len(xs)-1, len(ys)-1

	const (
		empty = -2
		wall  = -1
	)
	cell := make([]int, nx*ny) // room index (free floor), wall, or empty
	at := func(i, j int) int {
		if i < 0 || j < 0 || i >= nx || j >= ny {
			return empty
		}
		return cell[j*nx+i]
	}
	for j := 0; j < ny; j++ {
		for i := 0; i < nx; i++ {
			cx, cy := (xs[i]+xs[i+1])/2, (ys[j]+ys[j+1])/2
			v := empty
			owner := -1
			for k, r := range rooms {
				if r.contains(cx, cy) {
					owner = k
					break
				}
			}
			if owner >= 0 {
				v = owner
				for k, r := range rooms {
					if k != owner && r.grow(h).contains(cx, cy) {
						v = wall
						break
					}
				}
			} else {
				for _, r := range rooms {
					if r.grow(t).contains(cx, cy) {
						v = wall
						break
					}
				}
			}
			for _, b := range bands {
				if b.r.contains(cx, cy) {
					v = b.owner
				}
			}
			cell[j*nx+i] = v
		}
	}

	b := &builder{xs: xs, ys: ys, points: map[[2]int]int{}}
	full := func(i, j int) bool { return at(i, j) != empty }
	free := func(i, j int) bool { return at(i, j) >= 0 }

	for _, lp := range trace(nx, ny, full, func(i, j int) int { return 0 }) {
		b.addContour(lp, lp.area > 0, nil)
	}
	areaWalls := map[int][]int{}
	for _, lp := range trace(nx, ny, free, at) {
		b.addContour(lp, lp.area < 0, areaWalls)
	}

	// Cuts between neighbouring floor cells of different rooms.
	type key struct{ a, b int }
	areaCuts := map[int][]int{}
	addCut := func(p0, p1 [2]int, ra, rb int) {
		id := b.cut(p0, p1)
		areaCuts[ra] = append(areaCuts[ra], id)
		areaCuts[rb] = append(areaCuts[rb], id)
	}
	for i := 1; i < nx; i++ { // vertical grid lines
		start := -1
		var cur key
		for j := 0; j <= ny; j++ {
			var k key
			ok := false
			if j < ny {
				l, r := at(i-1, j), at(i, j)
				if l >= 0 && r >= 0 && l != r {
					k, ok = key{l, r}, true
				}
			}
			if start >= 0 && (!ok || k != cur) {
				addCut([2]int{i, start}, [2]int{i, j}, cur.a, cur.b)
				start = -1
			}
			if ok && start < 0 {
				start, cur = j, k
			}
		}
	}
	for j := 1; j < ny; j++ { // horizontal grid lines
		start := -1
		var cur key
		for i := 0; i <= nx; i++ {
			var k key
			ok := false
			if i < nx {
				d, u := at(i, j-1), at(i, j)
				if d >= 0 && u >= 0 && d != u {
					k, ok = key{d, u}, true
				}
			}
			if start >= 0 && (!ok || k != cur) {
				addCut([2]int{start, j}, [2]int{i, j}, cur.a, cur.b)
				start = -1
			}
			if ok && start < 0 {
				start, cur = i, k
			}
		}
	}

	res := &Result{}
	var areas []any
	for k, r := range s.Rooms {
		if len(areaWalls[k]) == 0 {
			res.Notes = append(res.Notes, fmt.Sprintf("room %q ended up with no floor", r.Name))
			continue
		}
		rh := r.Height
		if rh == 0 {
			rh = wh
		}
		areas = append(areas, map[string]any{
			"id": b.next(), "name": r.Name, "walls": ints(areaWalls[k]), "cuts": ints(areaCuts[k]),
			"h": rh, "ch": rh, "cv": 1, "cvisible": true, "cpv": false, "chbi": false,
			"m": floorColor, "addM": "", "mr": 0, "mx": 0, "my": 0,
			"mc": ceilingColor, "addMc": "", "mcr": 0, "mcx": 0, "mcy": 0,
		})
		// Clear inside size: the room rectangle minus partitions on shared sides.
		in := rooms[k]
		for q, o := range rooms {
			if q == k {
				continue
			}
			if eq(o.x1, in.x0) && overlaps(o.y0, o.y1, in.y0, in.y1) {
				in.x0 = rooms[k].x0 + h
			}
			if eq(o.x0, rooms[k].x1) && overlaps(o.y0, o.y1, in.y0, in.y1) {
				in.x1 = rooms[k].x1 - h
			}
			if eq(o.y1, rooms[k].y0) && overlaps(o.x0, o.x1, in.x0, in.x1) {
				in.y0 = rooms[k].y0 + h
			}
			if eq(o.y0, rooms[k].y1) && overlaps(o.x0, o.x1, in.x0, in.x1) {
				in.y1 = rooms[k].y1 - h
			}
		}
		res.Rooms = append(res.Rooms, RoomOut{Name: r.Name, Min: [2]float64{in.x0, in.y0}, Max: [2]float64{in.x1, in.y1},
			Center: [2]float64{(in.x0 + in.x1) / 2, (in.y0 + in.y1) / 2}})
	}

	c := plan.Construction(p)
	c["points"], c["walls"], c["cuts"], c["rooms"], c["areas"], c["covers"] = b.pointList, b.walls, b.cuts, b.rooms, areas, []any{}
	plan.SetWallsHeight(p, wh)
	centerCamera(p, rooms)
	res.Walls, res.Cuts = len(b.walls), len(b.cuts)
	return res, nil
}

const (
	wallColor    = "#efe9e1"
	floorColor   = "#b9895b"
	ceilingColor = "#ffffff"
)

func centerCamera(p plan.Plan, rooms []rect) {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, r := range rooms {
		x0, y0, x1, y1 = math.Min(x0, r.x0), math.Min(y0, r.y0), math.Max(x1, r.x1), math.Max(y1, r.y1)
	}
	sc := plan.Scene(p)
	vs, _ := sc["viewState"].(map[string]any)
	if vs == nil {
		vs = map[string]any{"state": "2d"}
		sc["viewState"] = vs
	}
	vs["camera"] = map[string]any{"px": (x0 + x1) / 2, "py": 0, "pz": -(y0 + y1) / 2, "tilt": 90, "pan": 0, "zoom": 0.1}
}

func eq(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func overlaps(a0, a1, b0, b1 float64) bool { return math.Min(a1, b1)-math.Max(a0, b0) > 0.01 }

func uniq(v []float64) []float64 {
	sort.Float64s(v)
	out := v[:0]
	for _, x := range v {
		if len(out) == 0 || !eq(out[len(out)-1], x) {
			out = append(out, x)
		}
	}
	return out
}

func ints(v []int) []any {
	out := make([]any, len(v))
	for i, x := range v {
		out[i] = x
	}
	return out
}

// ---- contour tracing ----

type loop struct {
	pts    [][2]int // grid vertices, after simplification
	labels []int    // label of the cell on the left of edge k (pts[k] → pts[k+1])
	area   float64  // signed, in grid-index units (positive = counter-clockwise)
}

// trace returns the boundary loops of the cells where in(i,j) holds, each oriented with the
// region on its left. Vertices are kept at corners and wherever the label on the left changes.
func trace(nx, ny int, in func(i, j int) bool, label func(i, j int) int) []loop {
	type edge struct {
		from, to [2]int
		lbl      int
	}
	out := map[[2]int][]int{}
	var edges []edge
	add := func(a, b [2]int, l int) {
		out[a] = append(out[a], len(edges))
		edges = append(edges, edge{a, b, l})
	}
	for j := 0; j < ny; j++ {
		for i := 0; i < nx; i++ {
			if !in(i, j) {
				continue
			}
			l := label(i, j)
			if !in(i, j-1) {
				add([2]int{i, j}, [2]int{i + 1, j}, l)
			}
			if !in(i+1, j) {
				add([2]int{i + 1, j}, [2]int{i + 1, j + 1}, l)
			}
			if !in(i, j+1) {
				add([2]int{i + 1, j + 1}, [2]int{i, j + 1}, l)
			}
			if !in(i-1, j) {
				add([2]int{i, j + 1}, [2]int{i, j}, l)
			}
		}
	}
	used := make([]bool, len(edges))
	dir := func(e edge) [2]int { return [2]int{sign(e.to[0] - e.from[0]), sign(e.to[1] - e.from[1])} }
	var loops []loop
	for s := range edges {
		if used[s] {
			continue
		}
		var seq []edge
		cur := s
		for !used[cur] {
			used[cur] = true
			e := edges[cur]
			seq = append(seq, e)
			next := -1
			d := dir(e)
			for _, cand := range out[e.to] {
				if used[cand] {
					continue
				}
				if next < 0 {
					next = cand
				}
				// at a pinch vertex prefer the left turn, which stays with the current region
				nd := dir(edges[cand])
				if d[0]*nd[1]-d[1]*nd[0] > 0 {
					next = cand
					break
				}
			}
			if next < 0 {
				break
			}
			cur = next
		}
		// simplify
		var lp loop
		n := len(seq)
		for k := 0; k < n; k++ {
			prev := seq[(k-1+n)%n]
			if dir(prev) != dir(seq[k]) || prev.lbl != seq[k].lbl {
				lp.pts = append(lp.pts, seq[k].from)
				lp.labels = append(lp.labels, seq[k].lbl)
			}
		}
		// The grid is monotonic in both axes, so the sign in index space is the real orientation.
		for k := range lp.pts {
			a, c := lp.pts[k], lp.pts[(k+1)%len(lp.pts)]
			lp.area += float64(a[0]*c[1]-c[0]*a[1]) / 2
		}
		loops = append(loops, lp)
	}
	return loops
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// ---- output ----

type builder struct {
	xs, ys    []float64
	id        int
	points    map[[2]int]int
	pointList []any
	walls     []any
	cuts      []any
	rooms     []any
}

func (b *builder) next() int { b.id++; return b.id }

func (b *builder) point(v [2]int) int {
	if id, ok := b.points[v]; ok {
		return id
	}
	id := b.next()
	b.points[v] = id
	b.pointList = append(b.pointList, map[string]any{"id": id, "x": b.xs[v[0]], "y": b.ys[v[1]], "type": "rp", "control": 0})
	return id
}

// addContour emits one wall contour. With areaWalls set, each wall is assigned to the room on
// its left.
func (b *builder) addContour(lp loop, outer bool, areaWalls map[int][]int) {
	n := len(lp.pts)
	if n < 3 {
		return
	}
	ids := make([]any, n)
	wallIDs := make([]any, n)
	for k := range lp.pts {
		ids[k] = b.point(lp.pts[k])
	}
	for k := range lp.pts {
		a, c := ids[k].(int), ids[(k+1)%n].(int)
		id := b.next()
		b.walls = append(b.walls, wallJSON(id, a, c))
		wallIDs[k] = id
		if areaWalls != nil && lp.labels[k] >= 0 {
			areaWalls[lp.labels[k]] = append(areaWalls[lp.labels[k]], id)
		}
	}
	o := 0
	if outer {
		o = 1
	}
	b.rooms = append(b.rooms, map[string]any{"id": b.next(), "points": ids, "walls": wallIDs, "outer": o})
}

func (b *builder) cut(p0, p1 [2]int) int {
	id := b.next()
	b.cuts = append(b.cuts, map[string]any{"id": id, "pa": b.point(p0), "pb": b.point(p1), "m": "0", "addM": "", "mr": 0, "mx": 0, "my": 0})
	return id
}

// Plinth shapes are the planner's defaults (WC.plinthCreator.defBottomId / defTopId).
const (
	plinthBottom = 42624
	plinthTop    = 42632
)

func wallJSON(id, a, b int) map[string]any {
	return map[string]any{
		"id": id, "partialVisible": false, "visible": true, "exists": true, "pa": a, "pb": b, "pm": "",
		"m": wallColor, "addM": "", "mr": 0, "mx": 0, "my": 0,
		"pltv": 0, "pltvisible": true, "pltpv": false, "plthbi": false, "pltm": "#ffffff", "pltAddm": "",
		"pltmr": 0, "pltmx": 0, "pltmy": 0, "plth": 0, "pltd": 0, "pltsh": plinthTop,
		"plbv": 1, "plbvisible": true, "plbpv": false, "plbhbi": false, "plbm": "#ffffff", "plbAddm": "",
		"plbmr": 0, "plbmx": 0, "plbmy": 0, "plbh": 0, "plbd": 0, "plbsh": plinthBottom,
		"conf": "",
	}
}
