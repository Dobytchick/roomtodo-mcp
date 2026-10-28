package roomtodo

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// TokenStore caches the roomtodo token on disk (0600) in the state directory.
type TokenStore struct{ Dir string }

func (s *TokenStore) file() string { return filepath.Join(s.Dir, "token.json") }

// Load returns the cached token, or "" if there is none.
func (s *TokenStore) Load() (string, error) {
	b, err := os.ReadFile(s.file())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var v struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	return v.Token, nil
}

// Save caches a token.
func (s *TokenStore) Save(token string) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]string{"token": token, "saved_at": time.Now().UTC().Format(time.RFC3339)})
	return os.WriteFile(s.file(), b, 0o600)
}

// Clear removes the cached token.
func (s *TokenStore) Clear() error {
	err := os.Remove(s.file())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
