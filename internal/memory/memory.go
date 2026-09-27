// Package memory provides a persistent, curated long-term memory store for
// the bot: free-form facts ("remember ...") plus named places ("sethome").
//
// This is the Bedrock equivalent of MinePal's Active Memory System /
// "Managing Memories": important moments, player preferences, and locations
// survive restarts via a JSON file and are injected into LLM prompts.
//
// It is a leaf package: it must not import internal/bot or any higher layer,
// so the architecture fitness sensor keeps passing.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxFacts bounds the fact list so the JSON file and prompt injection
	// stay small. Beyond the cap the oldest fact is dropped (FIFO).
	MaxFacts = 200
	// MaxFactLen caps a single fact in runes.
	MaxFactLen = 500
	// MaxPlaces bounds the named-place map.
	MaxPlaces = 50
	// DefaultRenderFacts is the default fact count for RenderForPrompt.
	DefaultRenderFacts = 10
)

// Fact is one curated memory entry.
type Fact struct {
	ID      int       `json:"id"`
	Text    string    `json:"text"`
	Author  string    `json:"author,omitempty"`
	SavedAt time.Time `json:"saved_at"`
}

// Place is a named location (e.g. "home").
type Place struct {
	Name    string    `json:"name"`
	X       float32   `json:"x"`
	Y       float32   `json:"y"`
	Z       float32   `json:"z"`
	SavedAt time.Time `json:"saved_at"`
}

type persistedStore struct {
	Facts  []Fact           `json:"facts"`
	Places map[string]Place `json:"places"`
	NextID int              `json:"next_id"`
}

// Store is a thread-safe fact + place store with JSON persistence.
type Store struct {
	mu     sync.RWMutex
	path   string
	facts  []Fact
	places map[string]Place
	nextID int
}

// New returns an empty store persisted at path. Use Load to populate it.
func New(path string) *Store {
	return &Store{
		path:   path,
		places: make(map[string]Place),
		nextID: 1,
	}
}

// Path returns the persistence file path.
func (s *Store) Path() string {
	return s.path
}

// Add stores a fact. Empty text and over-long text are rejected. When the
// store is full the oldest fact is evicted. Persistence is best-effort: the
// fact is kept in memory even when the file write fails.
func (s *Store) Add(text, author string) (Fact, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Fact{}, fmt.Errorf("memory text is empty")
	}
	if len([]rune(text)) > MaxFactLen {
		return Fact{}, fmt.Errorf("memory text too long (%d > %d chars)", len([]rune(text)), MaxFactLen)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := Fact{
		ID:      s.nextID,
		Text:    text,
		Author:  strings.TrimSpace(author),
		SavedAt: time.Now(),
	}
	s.nextID++
	s.facts = append(s.facts, f)
	for len(s.facts) > MaxFacts {
		s.facts = s.facts[1:]
	}
	_ = s.saveLocked()
	return f, nil
}

// Facts returns a copy of all facts, oldest first.
func (s *Store) Facts() []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Fact, len(s.facts))
	copy(out, s.facts)
	return out
}

// Search returns facts whose text contains query (case-insensitive).
// An empty query returns all facts.
func (s *Store) Search(query string) []Fact {
	query = strings.ToLower(strings.TrimSpace(query))
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Fact, 0, len(s.facts))
	for _, f := range s.facts {
		if query == "" || strings.Contains(strings.ToLower(f.Text), query) {
			out = append(out, f)
		}
	}
	return out
}

// Forget removes one fact: a numeric query matches by fact ID, otherwise the
// first fact whose text contains the query (case-insensitive) is removed.
func (s *Store) Forget(query string) (Fact, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Fact{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	if id, err := strconv.Atoi(query); err == nil {
		for i, f := range s.facts {
			if f.ID == id {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		lower := strings.ToLower(query)
		for i, f := range s.facts {
			if strings.Contains(strings.ToLower(f.Text), lower) {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return Fact{}, false
	}
	removed := s.facts[idx]
	s.facts = append(s.facts[:idx], s.facts[idx+1:]...)
	_ = s.saveLocked()
	return removed, true
}

// RememberPlace stores coordinates under a case-insensitive name.
func (s *Store) RememberPlace(name string, x, y, z float32) (Place, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return Place{}, fmt.Errorf("place name is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.places[name]; !ok && len(s.places) >= MaxPlaces {
		return Place{}, fmt.Errorf("too many places (max %d)", MaxPlaces)
	}
	p := Place{Name: name, X: x, Y: y, Z: z, SavedAt: time.Now()}
	s.places[name] = p
	_ = s.saveLocked()
	return p, nil
}

// Place returns the place stored under name (case-insensitive).
func (s *Store) Place(name string) (Place, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.places[name]
	return p, ok
}

// Places returns all places sorted by name.
func (s *Store) Places() []Place {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Place, 0, len(s.places))
	for _, p := range s.places {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RemovePlace deletes the named place.
func (s *Store) RemovePlace(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.places[name]; !ok {
		return false
	}
	delete(s.places, name)
	_ = s.saveLocked()
	return true
}

// RenderForPrompt renders a compact memory block for LLM system prompts.
// Returns "" when the store is empty. At most maxFacts facts are included;
// pass <= 0 for the default.
func (s *Store) RenderForPrompt(maxFacts int) string {
	if maxFacts <= 0 {
		maxFacts = DefaultRenderFacts
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.facts) == 0 && len(s.places) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[LONG-TERM MEMORY] Things players asked you to remember:\n")
	recent := s.facts
	if len(recent) > maxFacts {
		recent = recent[len(recent)-maxFacts:]
	}
	for _, f := range recent {
		if f.Author != "" {
			fmt.Fprintf(&b, "- (%d) %s (by %s)\n", f.ID, f.Text, f.Author)
		} else {
			fmt.Fprintf(&b, "- (%d) %s\n", f.ID, f.Text)
		}
	}
	if hidden := len(s.facts) - len(recent); hidden > 0 {
		fmt.Fprintf(&b, "(+%d more, use recall to list)\n", hidden)
	}
	if len(s.places) > 0 {
		names := make([]string, 0, len(s.places))
		for _, p := range s.places {
			names = append(names, fmt.Sprintf("%s (X:%.0f Y:%.0f Z:%.0f)", p.Name, p.X, p.Y, p.Z))
		}
		sort.Strings(names)
		b.WriteString("Known places: " + strings.Join(names, ", ") + ".")
	}
	return strings.TrimSpace(b.String())
}

// Save writes the store to its JSON file. Missing parent directories are
// created; the file keeps owner-only permissions like bot state files.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(persistedStore{
		Facts:  s.facts,
		Places: s.places,
		NextID: s.nextID,
	}, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Load reads the store from its JSON file. A missing file leaves the store
// empty and returns the read error so callers can distinguish first-run.
func (s *Store) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var persisted persistedStore
	if err := json.Unmarshal(data, &persisted); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts = persisted.Facts
	s.places = persisted.Places
	if s.places == nil {
		s.places = make(map[string]Place)
	}
	s.nextID = persisted.NextID
	if s.nextID < 1 {
		s.nextID = 1
	}
	for _, f := range s.facts {
		if f.ID >= s.nextID {
			s.nextID = f.ID + 1
		}
	}
	return nil
}
