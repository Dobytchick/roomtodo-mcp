package server

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Ready-made prompts the client shows as one-click starters.
var prompts = []struct {
	name, title, desc string
	args              []*mcp.PromptArgument
	text              string // {arg} placeholders are replaced with argument values
	defaults          map[string]string
}{
	{
		name: "one_room_apartment", title: "One-room apartment",
		desc: "Lay out and furnish a one-room apartment of a given size.",
		args: []*mcp.PromptArgument{
			{Name: "area_m2", Description: "total area in m², e.g. 40"},
			{Name: "file", Description: "project file name, e.g. apartment.json"},
		},
		defaults: map[string]string{"area_m2": "40", "file": "apartment.json"},
		text: `Design a one-room apartment of about {area_m2} m² in the roomtodo project "{file}".
1. create_floorplan: a living room with a sleeping area, a kitchen, a bathroom and an entrance hall, with doorways
   so every room is reachable from the hall. Keep rooms rectangular and touching; sizes in cm.
2. Put an entrance door into the hall's exterior wall and windows into the exterior walls of the living room and
   the kitchen (search_catalog for "дверь" and "окно", free_only).
3. Furnish with free catalog items: sofa, bed, wardrobe, dining table with chairs, kitchen unit, bathtub or shower,
   toilet, sink. Place each inside its room using the room bounds from get_project, rotated to face the room.
4. Finish with get_project and tell me how to open the file.`,
	},
	{
		name: "furnish_room", title: "Furnish a room",
		desc: "Furnish one room of an existing project in a chosen style.",
		args: []*mcp.PromptArgument{
			{Name: "project", Description: "project: .json file, account id or share link", Required: true},
			{Name: "room", Description: "room name, e.g. Bedroom", Required: true},
			{Name: "style", Description: "style, e.g. scandinavian, loft, minimalism"},
		},
		defaults: map[string]string{"style": "scandinavian"},
		text: `Furnish the room "{room}" in project "{project}" in a {style} style.
Read the room bounds with get_project first. Pick free catalog items that fit the style and the room size, leave
at least 70 cm of walking space, keep furniture against walls where it makes sense, and set floor and wall
materials or colours that match the style (set_material). Finish with get_project and a short list of what you placed.`,
	},
	{
		name: "house_with_corridor", title: "House with a corridor",
		desc: "A small house: two rooms joined by a corridor, with doors, windows and furniture.",
		args: []*mcp.PromptArgument{
			{Name: "file", Description: "project file name, e.g. house.json"},
		},
		defaults: map[string]string{"file": "house.json"},
		text: `Build a small house in the roomtodo project "{file}": a living room and a bedroom on both sides of a corridor,
with doorways from the corridor into both rooms. Add an entrance door into the corridor's exterior wall, a window
in every room's exterior wall, a sofa in the living room and a bed in the bedroom (free catalog items).
Finish with get_project and tell me how to open the file.`,
	},
}

func (s *Server) registerPrompts() {
	for _, p := range prompts {
		p := p
		s.mcp.AddPrompt(&mcp.Prompt{Name: p.name, Title: p.title, Description: p.desc, Arguments: p.args},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				text := p.text
				for _, a := range p.args {
					v := req.Params.Arguments[a.Name]
					if v == "" {
						v = p.defaults[a.Name]
					}
					text = strings.ReplaceAll(text, "{"+a.Name+"}", v)
				}
				return &mcp.GetPromptResult{Description: p.desc,
					Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}}, nil
			})
	}
}
