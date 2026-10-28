package floorplan

import (
	"math"
	"testing"

	"github.com/Dobytchick/roomtodo-mcp/internal/plan"
)

func house(t *testing.T, openings []Opening) (plan.Plan, *Result) {
	t.Helper()
	p := plan.New("test")
	res, err := Build(p, Spec{
		Rooms: []Room{
			{Name: "Living", X: 0, Y: 0, Width: 430, Depth: 660},
			{Name: "Hall", X: 430, Y: 0, Width: 160, Depth: 660},
			{Name: "Bedroom", X: 590, Y: 0, Width: 390, Depth: 660},
		},
		Openings: openings,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, res
}

func rooms(c map[string]any) (outer, inner int) {
	for _, r := range c["rooms"].([]any) {
		if r.(map[string]any)["outer"] == 1 {
			outer++
		} else {
			inner++
		}
	}
	return
}

func TestHouseWithDoorways(t *testing.T) {
	p, res := house(t, []Opening{{Between: [2]string{"Hall", "Living"}}, {Between: [2]string{"Hall", "Bedroom"}}})
	c := plan.Construction(p)
	if o, i := rooms(c); o != 1 || i != 1 {
		t.Fatalf("want 1 outer + 1 inner contour (rooms joined by doorways), got %d + %d", o, i)
	}
	if res.Cuts != 2 {
		t.Fatalf("want 2 cuts (one per doorway), got %d", res.Cuts)
	}
	s := plan.Summarize(p, false)
	want := map[string]float64{
		"Living":  (430 - 10) * 660,       // loses half a partition on one side
		"Hall":    (160-20)*660 + 2*90*20, // two partitions, plus both doorway strips (full thickness)
		"Bedroom": (390 - 10) * 660,
	}
	if len(s.Rooms) != 3 {
		t.Fatalf("want 3 rooms, got %d", len(s.Rooms))
	}
	for _, r := range s.Rooms {
		w := want[r.Name] / 10000
		if math.Abs(r.AreaM2-w) > 0.02 {
			t.Errorf("%s: area %.2f m², want %.2f", r.Name, r.AreaM2, w)
		}
	}
}

func TestSeparateRoomGetsOwnContour(t *testing.T) {
	p, res := house(t, []Opening{{Between: [2]string{"Hall", "Living"}}})
	if o, i := rooms(plan.Construction(p)); o != 1 || i != 2 {
		t.Fatalf("want 1 outer + 2 inner contours, got %d + %d", o, i)
	}
	if res.Cuts != 1 {
		t.Fatalf("want 1 cut, got %d", res.Cuts)
	}
}

func TestDoorSnapsToWall(t *testing.T) {
	p, _ := house(t, nil)
	// a point near the front (y=-20..0) exterior wall of the hall
	spot, err := plan.FindWallSpot(p, plan.Pt{X: 510, Y: -5})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(spot.Center.Y+10) > 0.01 || math.Abs(spot.Thickness-20) > 0.01 {
		t.Fatalf("want centre line y=-10, thickness 20; got %+v", spot)
	}
	// a partition between Living and Hall (x=420..440)
	spot, err = plan.FindWallSpot(p, plan.Pt{X: 425, Y: 300})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(spot.Center.X-430) > 0.01 || math.Abs(spot.Thickness-20) > 0.01 {
		t.Fatalf("want centre line x=430, thickness 20; got %+v", spot)
	}
}

func TestMoveAndRemoveObjects(t *testing.T) {
	p, _ := house(t, nil)
	window := map[string]any{"id": 235.0, "type": 2.0, "plan": "free",
		"property": map[string]any{"appointment": "wall", "sizes": map[string]any{"width": 143.0, "height": 135.0, "depth": 22.0}}}
	sofa := map[string]any{"id": 951.0, "type": 2.0, "property": map[string]any{"sizes": map[string]any{"width": 259.0, "height": 91.0, "depth": 106.0}}}

	w, spot, err := plan.AddProduct(p, window, plan.Placement{X: 200, Y: -3, Elevation: 80})
	if err != nil {
		t.Fatal(err)
	}
	if spot == nil || w["z"] != 10.0 || w["depth"] != 21.0 {
		t.Fatalf("window should snap to the front wall centre (z=10) with depth 21: %v", w)
	}
	s, _, err := plan.AddProduct(p, sofa, plan.Placement{X: 200, Y: 300})
	if err != nil {
		t.Fatal(err)
	}

	// Moving a wall item along the wall keeps it in the wall.
	x := 300.0
	y := 5.0
	if _, err := plan.UpdateProduct(p, plan.Summarize(p, false).Objects[0].ObjID, plan.Patch{X: &x, Y: &y}); err != nil {
		t.Fatal(err)
	}
	if w["x"] != 300.0 || w["z"] != 10.0 {
		t.Fatalf("moved window left the wall: %v", w)
	}
	rot, sc := 90.0, 2.0
	if _, err := plan.UpdateProduct(p, s["objid"].(string), plan.Patch{Rotation: &rot, ScaleX: &sc}); err != nil {
		t.Fatal(err)
	}
	if s["ry"] != 90.0 || s["width"] != 518.0 {
		t.Fatalf("sofa rotate/scale: %v", s)
	}
	if _, err := plan.RemoveProduct(p, s["objid"].(string)); err != nil {
		t.Fatal(err)
	}
	if n := len(plan.Products(p)); n != 1 {
		t.Fatalf("want 1 object left, got %d", n)
	}
}

func TestOverlapRejected(t *testing.T) {
	_, err := Build(plan.New(""), Spec{Rooms: []Room{{Name: "A", Width: 300, Depth: 300}, {Name: "B", X: 100, Width: 300, Depth: 300}}})
	if err == nil {
		t.Fatal("want overlap error")
	}
}
