package roomtodo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// BrowserLogin signs in without copy-pasting: it opens a real Chrome/Edge window with its own
// persistent profile on the planner, lets the user sign in however they like, then reads the
// token from the page's localStorage over the Chrome DevTools Protocol. The profile persists,
// so later refreshes usually need no user action.
type BrowserLogin struct {
	Domain  string
	SiteKey string
	// StateDir holds the browser profile and the remembered DevTools port.
	StateDir string
	// Browser is the Chrome/Edge executable; empty means auto-detect.
	Browser string
	Timeout time.Duration
	Log     func(string)
}

func (b *BrowserLogin) profileDir() string { return filepath.Join(b.StateDir, "browser-profile") }
func (b *BrowserLogin) portFile() string   { return filepath.Join(b.StateDir, "devtools-port") }

// Token implements TokenSource.
func (b *BrowserLogin) Token(ctx context.Context) (string, error) {
	timeout := b.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := os.MkdirAll(b.profileDir(), 0o700); err != nil {
		return "", err
	}
	plannerURL := strings.TrimRight(b.Domain, "/") + "/planner"

	// If our profile is already open (e.g. an earlier login attempt), a second launch just hands
	// the URL to that process and ignores the new --remote-debugging-port, so reuse its port
	// instead of waiting on a port nobody listens on.
	port := b.runningPort(ctx)
	if port != 0 {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d/json/new?%s", port, plannerURL), nil)
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
		b.log(fmt.Sprintf("reusing the open sign-in browser (port %d); sign in at %s", port, plannerURL))
	} else {
		exe, err := b.findBrowser()
		if err != nil {
			return "", err
		}
		port, err = freePort()
		if err != nil {
			return "", err
		}
		cmd := exec.Command(exe,
			"--remote-debugging-port="+strconv.Itoa(port),
			"--user-data-dir="+b.profileDir(),
			"--no-first-run", "--no-default-browser-check", "--new-window", plannerURL)
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("start browser: %w", err)
		}
		go cmd.Wait() //nolint:errcheck // reap the process; the browser outlives this call
		_ = os.WriteFile(b.portFile(), []byte(strconv.Itoa(port)), 0o600)
		b.log(fmt.Sprintf("opened %s; sign in at %s", exe, plannerURL))
	}

	host := hostOf(b.Domain)
	expr := fmt.Sprintf(`location.hostname === %q ? localStorage.getItem(%q) : null`, host, "r2d_token_"+b.SiteKey)
	for {
		select {
		case <-ctx.Done():
			return "", errors.New("timed out waiting for roomtodo sign-in in the browser window")
		case <-time.After(time.Second):
		}
		token, err := evalOnPage(ctx, port, host, expr)
		if err != nil || token == "" {
			continue // browser starting, page navigating, user not signed in yet
		}
		closeBrowser(ctx, port)
		return token, nil
	}
}

func (b *BrowserLogin) log(s string) {
	if b.Log != nil {
		b.Log(s)
	}
}

func (b *BrowserLogin) runningPort(ctx context.Context) int {
	raw, err := os.ReadFile(b.portFile())
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if port == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/version", port), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0
	}
	return port
}

func (b *BrowserLogin) findBrowser() (string, error) {
	if b.Browser != "" {
		return b.Browser, nil
	}
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		pf, pf86, local := os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")
		candidates = []string{
			filepath.Join(pf, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(pf86, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(pf86, `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(pf, `Microsoft\Edge\Application\msedge.exe`),
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge"} {
			if p, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, p)
			}
		}
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("Chrome/Edge not found: set ROOMTODO_BROWSER to the browser executable, or pass a token instead")
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func hostOf(domain string) string {
	h := strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	if i := strings.IndexAny(h, "/:"); i >= 0 {
		h = h[:i]
	}
	return h
}

type cdpTarget struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	WS   string `json:"webSocketDebuggerUrl"`
}

func getJSON(ctx context.Context, url string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(out)
}

// evalOnPage evaluates expr in the first page on host and returns its string value.
func evalOnPage(ctx context.Context, port int, host, expr string) (string, error) {
	var targets []cdpTarget
	if err := getJSON(ctx, fmt.Sprintf("http://127.0.0.1:%d/json/list", port), &targets); err != nil {
		return "", err
	}
	var ws string
	for _, t := range targets {
		if t.Type == "page" && strings.Contains(t.URL, "://"+host) {
			ws = t.WS
			break
		}
	}
	if ws == "" {
		return "", errors.New("no planner page open")
	}
	var res struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if err := cdpCall(ctx, ws, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &res); err != nil {
		return "", err
	}
	s, _ := res.Result.Value.(string)
	return s, nil
}

func closeBrowser(ctx context.Context, port int) {
	var v struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if getJSON(ctx, fmt.Sprintf("http://127.0.0.1:%d/json/version", port), &v) != nil || v.WS == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = cdpCall(ctx, v.WS, "Browser.close", nil, nil)
}

// cdpCall sends one CDP command over a fresh websocket and waits for its reply.
func cdpCall(ctx context.Context, wsURL, method string, params any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return err
	}
	defer c.CloseNow()
	msg, _ := json.Marshal(map[string]any{"id": 1, "method": method, "params": params})
	if err := c.Write(ctx, websocket.MessageText, msg); err != nil {
		return err
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &reply) != nil || reply.ID != 1 {
			continue // an event, not our reply
		}
		if reply.Error != nil {
			return errors.New(reply.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(reply.Result, out)
		}
		return nil
	}
}
