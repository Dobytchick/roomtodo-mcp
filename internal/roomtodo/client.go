// Package roomtodo is a thin client for the roomtodo.com planner API, reconstructed from the
// site's own JavaScript (R2D.URL map on /planner, plannercore.js, user.js). It is unofficial
// and may break whenever the site changes.
package roomtodo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultDomain = "https://roomtodo.com"
	// DefaultSiteKey is the public site key embedded in every planner URL (?key=...).
	DefaultSiteKey = "4500282e6846fe6650de81bd35d27540"
)

// Error is an error reported by the roomtodo API.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

// AuthError means the request needs a valid token.
type AuthError struct{ Msg string }

func (e *AuthError) Error() string { return e.Msg }

var authHint = regexp.MustCompile(`(?i)token|auth|unauthori[sz]ed|not logged|login|forbidden|no user found`)

// TokenSource obtains a token interactively (e.g. browser sign-in).
type TokenSource func(ctx context.Context) (string, error)

// Client talks to the roomtodo API. It is safe for concurrent use.
type Client struct {
	Domain  string
	SiteKey string
	Lang    string
	HTTP    *http.Client
	Store   *TokenStore
	// Login is called when an authenticated call has no token; nil disables interactive login.
	Login TokenSource
	Log   func(string)

	mu       sync.Mutex
	token    string
	envToken string
	loginMu  sync.Mutex
}

// New returns a client. envToken, if set, is used as-is and never replaced by a browser login.
func New(envToken string, store *TokenStore) *Client {
	return &Client{
		Domain:   DefaultDomain,
		SiteKey:  DefaultSiteKey,
		Lang:     "ru",
		HTTP:     &http.Client{Timeout: 60 * time.Second},
		Store:    store,
		Log:      func(string) {},
		token:    envToken,
		envToken: envToken,
	}
}

// HasToken reports whether a token is available without interactive login.
func (c *Client) HasToken() bool {
	c.mu.Lock()
	t := c.token
	c.mu.Unlock()
	if t != "" {
		return true
	}
	if c.Store != nil {
		if t, _ := c.Store.Load(); t != "" {
			return true
		}
	}
	return false
}

// SetToken uses the given token for this process (and caches it when persist is true).
func (c *Client) SetToken(token string, persist bool) error {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
	if persist && c.Store != nil {
		return c.Store.Save(token)
	}
	return nil
}

// EnsureToken returns a token: env/explicit token → cached token → interactive login.
func (c *Client) EnsureToken(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	t := c.token
	c.mu.Unlock()
	if !force && t != "" {
		return t, nil
	}
	if !force && c.Store != nil {
		if t, _ := c.Store.Load(); t != "" {
			c.mu.Lock()
			c.token = t
			c.mu.Unlock()
			return t, nil
		}
	}
	if c.Login == nil {
		return "", &AuthError{"not signed in: call the login tool, pass a token, or set ROOMTODO_TOKEN"}
	}
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	t, err := c.Login(ctx)
	if err != nil {
		return "", err
	}
	if c.Store != nil {
		if err := c.Store.Save(t); err != nil {
			c.Log("could not cache token: " + err.Error())
		}
	}
	c.mu.Lock()
	c.token = t
	c.mu.Unlock()
	return t, nil
}

// Logout forgets the cached token.
func (c *Client) Logout() error {
	c.mu.Lock()
	c.token = c.envToken
	c.mu.Unlock()
	if c.Store != nil {
		return c.Store.Clear()
	}
	return nil
}

type request struct {
	method string
	path   string
	query  url.Values
	body   string // pre-encoded x-www-form-urlencoded body
	authed bool
}

func jsonForm(v any) string {
	b, _ := json.Marshal(v)
	return "json=" + url.QueryEscape(string(b))
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	if r.authed {
		if _, err := c.EnsureToken(ctx, false); err != nil {
			return err
		}
	}
	err := c.send(ctx, r, out)
	var ae *AuthError
	if !errors.As(err, &ae) || c.envToken != "" || c.Login == nil {
		return err
	}
	// Stale token: sign in again once (the browser profile usually makes this silent).
	c.Log("token rejected, signing in again")
	if c.Store != nil {
		_ = c.Store.Clear()
	}
	if _, err := c.EnsureToken(ctx, true); err != nil {
		return err
	}
	return c.send(ctx, r, out)
}

func (c *Client) send(ctx context.Context, r request, out any) error {
	q := url.Values{}
	for k, v := range r.query {
		if len(v) > 0 && v[0] != "" {
			q[k] = v
		}
	}
	q.Set("key", c.SiteKey)
	u := strings.TrimRight(c.Domain, "/") + r.path + "?" + q.Encode()

	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u, body)
	if err != nil {
		return err
	}
	if r.body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c.mu.Lock()
	token := c.token
	c.mu.Unlock()
	if token != "" {
		req.Header.Set("x-token", token)
	}
	if c.Lang != "" {
		req.Header.Set("x-lang", c.Lang)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if r.authed && (res.StatusCode == 401 || res.StatusCode == 403) {
		return &AuthError{fmt.Sprintf("HTTP %d: %s", res.StatusCode, clip(string(raw), 300))}
	}
	var envelope struct {
		Status  string `json:"status"`
		Error   any    `json:"error"`
		Message any    `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return &Error{fmt.Sprintf("HTTP %d: non-JSON response: %s", res.StatusCode, clip(string(raw), 300))}
	}
	if res.StatusCode >= 400 {
		return &Error{fmt.Sprintf("HTTP %d: %s", res.StatusCode, clip(string(raw), 500))}
	}
	if envelope.Status == "error" {
		msg := fmt.Sprint(firstNonNil(envelope.Error, envelope.Message, clip(string(raw), 500)))
		if r.authed && authHint.MatchString(msg) {
			return &AuthError{msg}
		}
		return &Error{msg}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil && v != "" {
			return v
		}
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ---- user ----

// UserInfo returns the signed-in user's profile.
func (c *Client) UserInfo(ctx context.Context) (map[string]any, error) {
	var r map[string]any
	err := c.do(ctx, request{method: "GET", path: "/api2/user/info", authed: true}, &r)
	return r, err
}

// ---- projects ----

// ListProjects lists the user's projects.
func (c *Client) ListProjects(ctx context.Context, limit, offset int) (map[string]any, error) {
	var r map[string]any
	err := c.do(ctx, request{method: "GET", path: "/api2/planner/list", authed: true,
		query: url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}}, &r)
	return r, err
}

// Opened is a project returned by open / open_shared.
type Opened struct {
	Plan     map[string]any
	ViewOnly bool
}

type openResponse struct {
	Status   string          `json:"status"`
	Plan     json.RawMessage `json:"plan"`
	ViewOnly any             `json:"viewOnly"`
}

func parsePlan(r openResponse) (*Opened, error) {
	if r.Status != "success" || len(r.Plan) == 0 {
		return nil, &Error{"unexpected open response"}
	}
	raw := r.Plan
	var s string
	if json.Unmarshal(raw, &s) == nil { // plan may arrive as a JSON string
		raw = json.RawMessage(s)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, fmt.Errorf("decode plan: %w", err)
	}
	vo, _ := r.ViewOnly.(bool)
	return &Opened{Plan: plan, ViewOnly: vo}, nil
}

// OpenProject loads one of the user's projects.
func (c *Client) OpenProject(ctx context.Context, planID string) (*Opened, error) {
	var r openResponse
	if err := c.do(ctx, request{method: "POST", path: "/api/planner/open", authed: true,
		body: jsonForm(map[string]any{"plan_id": planID})}, &r); err != nil {
		return nil, err
	}
	return parsePlan(r)
}

// OpenShared loads a project by its public share hash (no sign-in needed).
func (c *Client) OpenShared(ctx context.Context, hash string) (*Opened, error) {
	var r openResponse
	if err := c.do(ctx, request{method: "POST", path: "/api/planner/open_shared",
		query: url.Values{"plan_hash": {hash}},
		body:  jsonForm(map[string]any{"plan_hash": hash})}, &r); err != nil {
		return nil, err
	}
	return parsePlan(r)
}

// SaveProject saves a plan; a plan with id 0 creates a new project. Returns the plan id.
func (c *Client) SaveProject(ctx context.Context, plan map[string]any) (string, error) {
	var r struct {
		Status string `json:"status"`
		PlanID any    `json:"plan_id"`
	}
	// SceneSaver: "json=" + encodeURIComponent(JSON.stringify({plan})).
	if err := c.do(ctx, request{method: "POST", path: "/api/planner/save", authed: true,
		body: jsonForm(map[string]any{"plan": plan})}, &r); err != nil {
		return "", err
	}
	if r.Status != "success" {
		return "", &Error{"save failed"}
	}
	return fmt.Sprint(r.PlanID), nil
}

func (c *Client) planCall(ctx context.Context, path string, args map[string]any) (map[string]any, error) {
	var r map[string]any
	err := c.do(ctx, request{method: "POST", path: path, authed: true, body: jsonForm(args)}, &r)
	return r, err
}

// RenameProject renames a project.
func (c *Client) RenameProject(ctx context.Context, id, name, oldName string) (map[string]any, error) {
	return c.planCall(ctx, "/api/planner/rename", map[string]any{"plan_id": id, "plan_name": name, "plan_old_name": oldName})
}

// CopyProject duplicates a project.
func (c *Client) CopyProject(ctx context.Context, id, newName, oldName string) (map[string]any, error) {
	return c.planCall(ctx, "/api/planner/copy", map[string]any{"plan_id": id, "plan_new_name": newName, "plan_old_name": oldName})
}

// DeleteProject deletes a project.
func (c *Client) DeleteProject(ctx context.Context, id string) (map[string]any, error) {
	return c.planCall(ctx, "/api/planner/delete", map[string]any{"plan_id": id})
}

// ShareLink makes a project public and returns the API response (contains the hash).
func (c *Client) ShareLink(ctx context.Context, id string) (map[string]any, error) {
	return c.planCall(ctx, "/api/planner/shareLink", map[string]any{"plan_id": id})
}

// ---- catalog (public) ----

// CatalogQuery filters a catalog search.
type CatalogQuery struct {
	Query        string
	IDs          []string
	CategoryTag  string
	CategoryType int
	Limit        int
	Offset       int
}

// CatalogSearch searches furniture, materials, doors and windows.
func (c *Client) CatalogSearch(ctx context.Context, q CatalogQuery) ([]map[string]any, int, error) {
	v := url.Values{}
	v.Set("query", q.Query)
	v.Set("ids", strings.Join(q.IDs, ","))
	v.Set("category_tag", q.CategoryTag)
	if q.CategoryType != 0 {
		v.Set("category_type", strconv.Itoa(q.CategoryType))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	var r struct {
		Data struct {
			Total any             `json:"total"`
			Items json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := c.do(ctx, request{method: "GET", path: "/api/catalog/search", query: v}, &r); err != nil {
		return nil, 0, err
	}
	var items []map[string]any
	if err := json.Unmarshal(r.Data.Items, &items); err != nil {
		var byID map[string]map[string]any // some responses key items by id
		if json.Unmarshal(r.Data.Items, &byID) == nil {
			for _, it := range byID {
				items = append(items, it)
			}
		}
	}
	total, _ := strconv.Atoi(fmt.Sprint(r.Data.Total))
	return items, total, nil
}

// Product fetches one catalog item by id.
func (c *Client) Product(ctx context.Context, id string) (map[string]any, error) {
	items, _, err := c.CatalogSearch(ctx, CatalogQuery{IDs: []string{id}})
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if fmt.Sprint(it["id"]) == id {
			return it, nil
		}
	}
	return nil, &Error{fmt.Sprintf("product %s not found in the catalog", id)}
}

// CategoryTree returns the catalog category tree.
func (c *Client) CategoryTree(ctx context.Context) (map[string]any, error) {
	var r map[string]any
	err := c.do(ctx, request{method: "GET", path: "/api/category/tree"}, &r)
	return r, err
}

// ---- renders ----

// ListRenders lists the user's renders.
func (c *Client) ListRenders(ctx context.Context) (map[string]any, error) {
	var r map[string]any
	err := c.do(ctx, request{method: "GET", path: "/api/render", authed: true}, &r)
	return r, err
}

// GetRender returns one render.
func (c *Client) GetRender(ctx context.Context, id string) (map[string]any, error) {
	var r map[string]any
	err := c.do(ctx, request{method: "GET", path: "/api/render/" + url.PathEscape(id), authed: true}, &r)
	return r, err
}
