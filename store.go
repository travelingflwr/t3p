package main

// What t3p knows that neither the folders nor T3 do: a note for each place,
// its tags, whether it's open, parked or closed, a case's severity and last
// update, and the worktree each T3 project was last opened in. One JSON file.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Meta struct {
	Note       string     `json:"note,omitempty"`
	Tags       []string   `json:"tags,omitempty"`
	Status     string     `json:"status,omitempty"` // "" (open), "parked", "closed"
	Sev        int        `json:"sev,omitempty"`
	Created    *time.Time `json:"created,omitempty"`
	LastUpdate *time.Time `json:"last_update,omitempty"` // the last update sent (cases)
	Ignore     []string   `json:"ignore,omitempty"`      // Tidy Up checks not to raise again
}

func (m *Meta) empty() bool {
	return m.Note == "" && len(m.Tags) == 0 && m.Status == "" && m.Sev == 0 &&
		m.Created == nil && m.LastUpdate == nil && len(m.Ignore) == 0
}

func (m *Meta) ignores(check string) bool {
	for _, c := range m.Ignore {
		if c == check || c == "*" {
			return true
		}
	}
	return false
}

type Store struct {
	Places map[string]*Meta  `json:"places"`
	Last   map[string]string `json:"last"` // T3 project "env/id" → workspace id

	path string
}

func loadStore() (*Store, error) {
	s := &Store{Places: map[string]*Meta{}, Last: map[string]string{},
		path: filepath.Join(stateDir(), "places.json")}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		// Never let a broken file stop work: start over, keeping the old one.
		_ = os.Rename(s.path, s.path+".broken")
		return &Store{Places: map[string]*Meta{}, Last: map[string]string{}, path: s.path}, nil
	}
	if s.Places == nil {
		s.Places = map[string]*Meta{}
	}
	if s.Last == nil {
		s.Last = map[string]string{}
	}
	return s, nil
}

// meta returns key's entry, making it.
func (s *Store) meta(key string) *Meta {
	m := s.Places[key]
	if m == nil {
		m = &Meta{}
		s.Places[key] = m
	}
	return m
}

// peek returns key's entry or an empty one, without adding it.
func (s *Store) peek(key string) Meta {
	if m := s.Places[key]; m != nil {
		return *m
	}
	return Meta{}
}

func (s *Store) save() error {
	for k, m := range s.Places {
		if m.empty() {
			delete(s.Places, k)
		}
	}
	for _, m := range s.Places {
		sort.Strings(m.Tags)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	// Write then rename, so a crash never leaves half a file.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
