package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// The household's profiles, created and picked in Lampa: Lampac keeps bookmarks and positions per
// lampac_profile_id but has no list of profiles or a picker. The gateway keeps the list in its data volume so
// a profile made on one device appears on all. The main profile has the empty id (the household's shared data)
// and cannot be removed. Ids are never reused, so a new profile cannot inherit a removed one's data.

const maxProfiles = 8

type profile struct {
	ID   string `json:"id"`
	Name string `json:"name"` // empty for the main profile: the plugin shows its translated default name
}

type profileStore struct {
	mu   sync.Mutex
	path string
	data struct {
		Next     int       `json:"next"`
		Profiles []profile `json:"profiles"`
	}
}

func newProfileStore(dir string) *profileStore {
	s := &profileStore{path: filepath.Join(dir, "profiles.json")}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.data)
	}
	if len(s.data.Profiles) == 0 || s.data.Profiles[0].ID != "" {
		s.data.Profiles = append([]profile{{}}, s.data.Profiles...)
	}
	if s.data.Next < 2 {
		s.data.Next = 2
	}
	return s
}

func cleanName(name string) (string, error) {
	name = strings.Join(strings.FieldsFunc(name, unicode.IsControl), " ")
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 24 {
		return "", errors.New("name must be 1 to 24 characters")
	}
	return name, nil
}

func (s *profileStore) apply(action, id, name string) error {
	switch action {
	case "add":
		n, err := cleanName(name)
		if err != nil {
			return err
		}
		if len(s.data.Profiles) >= maxProfiles {
			return errors.New("at most 8 profiles")
		}
		s.data.Profiles = append(s.data.Profiles, profile{ID: strconv.Itoa(s.data.Next), Name: n})
		s.data.Next++
	case "rename":
		n, err := cleanName(name)
		if err != nil {
			return err
		}
		for i := range s.data.Profiles {
			if s.data.Profiles[i].ID == id {
				s.data.Profiles[i].Name = n
				return nil
			}
		}
		return errors.New("no such profile")
	case "remove":
		for i, p := range s.data.Profiles {
			if p.ID == id && id != "" {
				s.data.Profiles = append(s.data.Profiles[:i], s.data.Profiles[i+1:]...)
				return nil
			}
		}
		return errors.New("no such profile, or the main one")
	default:
		return errors.New("unknown action")
	}
	return nil
}

func (s *profileStore) save() error {
	b, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// handle: GET lists the profiles; POST {"action": "add"|"rename"|"remove", "id", "name"} changes them and
// answers with the new list. The body is read as JSON whatever its content type, so the plugin can send it as
// text/plain and stock TV apps need no CORS preflight.
func (s *profileStore) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*") // stock TV apps call from their own origin
	w.Header().Set("Cache-Control", "no-store")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodPost:
		var req struct{ Action, ID, Name string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		before := s.data
		before.Profiles = append([]profile(nil), s.data.Profiles...)
		if err := s.apply(req.Action, req.ID, req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.save(); err != nil {
			slog.Error("profiles", "err", err)
			s.data = before
			http.Error(w, "could not save", http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.data.Profiles)
}
