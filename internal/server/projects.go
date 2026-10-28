package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Dobytchick/roomtodo-mcp/internal/plan"
)

// A project reference is one of:
//   - an account project id: "799253"
//   - a public share link or hash: "https://roomtodo.com/planner/project/qfbypax-fk3w", "share:qfbypax-fk3w"
//   - a local file: "house.json" (relative names live in the projects folder) or an absolute path
type ref struct {
	kind string // "account", "share", "file", "new"
	id   string // project id, share hash, or file path
}

var (
	reID      = regexp.MustCompile(`^\d+$`)
	reShareIn = regexp.MustCompile(`/project/([A-Za-z0-9_-]+)`)
)

func (s *Server) parseRef(v string) (ref, error) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return ref{}, errors.New("project is required: an account project id, a share link, or a .json file name")
	case v == "new" || v == "account":
		return ref{kind: "new"}, nil
	case reID.MatchString(v):
		return ref{kind: "account", id: v}, nil
	case strings.HasPrefix(v, "share:"):
		return ref{kind: "share", id: strings.TrimPrefix(v, "share:")}, nil
	case strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://"):
		if m := reShareIn.FindStringSubmatch(v); m != nil {
			return ref{kind: "share", id: m[1]}, nil
		}
		return ref{}, fmt.Errorf("not a roomtodo project link: %s", v)
	}
	p, err := s.filePath(v)
	if err != nil {
		return ref{}, err
	}
	return ref{kind: "file", id: p}, nil
}

// filePath resolves a project file: absolute paths as given, plain names inside the projects folder.
func (s *Server) filePath(name string) (string, error) {
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name += ".json"
	}
	if filepath.IsAbs(name) {
		return filepath.Clean(name), nil
	}
	clean := filepath.Clean(name)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", errors.New("file names must stay inside the projects folder")
	}
	return filepath.Join(s.cfg.ProjectsDir, clean), nil
}

func (s *Server) load(ctx context.Context, r ref) (plan.Plan, error) {
	switch r.kind {
	case "account":
		o, err := s.api.OpenProject(ctx, r.id)
		if err != nil {
			return nil, err
		}
		return o.Plan, nil
	case "share":
		o, err := s.api.OpenShared(ctx, r.id)
		if err != nil {
			return nil, err
		}
		return o.Plan, nil
	case "file":
		b, err := os.ReadFile(r.id)
		if errors.Is(err, os.ErrNotExist) { // editing a file that doesn't exist yet starts a project
			return plan.New(strings.TrimSuffix(filepath.Base(r.id), filepath.Ext(r.id))), nil
		}
		if err != nil {
			return nil, err
		}
		return plan.Parse(b)
	case "new":
		return plan.New(""), nil
	}
	return nil, errors.New("bad project reference")
}

// Saved tells the model where a project went and how to open it.
type Saved struct {
	Where     string `json:"saved_to"`
	ProjectID string `json:"project_id,omitempty"`
	File      string `json:"file,omitempty"`
	OpenURL   string `json:"open_url,omitempty"`
	HowToOpen string `json:"how_to_open,omitempty"`
}

const openFileHint = "Open https://roomtodo.com/planner (no sign-in needed), press Ctrl+Alt+O and pick this file."

func (s *Server) save(ctx context.Context, r ref, p plan.Plan) (*Saved, error) {
	switch r.kind {
	case "share":
		return nil, errors.New("shared projects are read-only: pass save_to (a .json file name, or \"new\" to copy it into your account)")
	case "account", "new":
		if r.kind == "new" {
			p["id"] = 0
			p["hash"] = 0
		} else {
			p["id"] = r.id
		}
		id, err := s.api.SaveProject(ctx, p)
		if err != nil {
			return nil, err
		}
		out := &Saved{Where: "account", ProjectID: id}
		if o, err := s.api.OpenProject(ctx, id); err == nil {
			if h := fmt.Sprint(o.Plan["hash"]); h != "" && h != "0" && h != "<nil>" {
				out.OpenURL = "https://roomtodo.com/planner/project/" + h
			}
		}
		if out.OpenURL == "" {
			out.HowToOpen = "Open https://roomtodo.com/planner and pick it under My projects."
		}
		return out, nil
	case "file":
		if err := os.MkdirAll(filepath.Dir(r.id), 0o755); err != nil {
			return nil, err
		}
		b, err := marshalIndent(p)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(r.id, b, 0o644); err != nil {
			return nil, err
		}
		return &Saved{Where: "file", File: r.id, HowToOpen: openFileHint}, nil
	}
	return nil, errors.New("bad project reference")
}

// edit loads a project, applies fn and saves it (to saveTo when given, else back in place).
func (s *Server) edit(ctx context.Context, project, saveTo string, fn func(plan.Plan) (any, error)) (map[string]any, error) {
	src, err := s.parseRef(project)
	if err != nil {
		return nil, err
	}
	dst := src
	if saveTo != "" {
		if dst, err = s.parseRef(saveTo); err != nil {
			return nil, err
		}
	}
	p, err := s.load(ctx, src)
	if err != nil {
		return nil, err
	}
	result, err := fn(p)
	if err != nil {
		return nil, err
	}
	saved, err := s.save(ctx, dst, p)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"saved": saved, "project": plan.Summarize(p, false)}
	if result != nil {
		out["result"] = result
	}
	return out, nil
}
