// Package memory provides a persistent, curated long-term memory store for
// the bot: free-form facts ("remember ...") plus named places ("sethome").
//
// This is the Bedrock equivalent of MinePal's Active Memory System /
// "Managing Memories": important moments, player preferences, and locations
// survive restarts via a JSON file and are injected into LLM prompts.
//
// Memories are kept per server. Every bucket is keyed by the host:port the
// bot is connected to, and SetServer points the store at the bucket for the
// live session — rules dictated on one server never leak into another, and a
// "home" remembered on one hub is not walked to on the next. The shared
// bucket (empty key) holds memories recorded before per-server memory existed
// and anything saved while no server was known.
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
	// SharedBucket is the key memories live under when no server is known:
	// legacy stores loaded from the flat pre-per-server format, and anything
	// saved by a caller that never called SetServer.
	SharedBucket = ""
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

// bucket is one server's slice of the store: its own facts, its own named
// places, and its own fact numbering — an ID is only ever used to forget a
// fact from the server the player is looking at.
type bucket struct {
	facts  []Fact
	places map[string]Place
	nextID int
}

func newBucket() *bucket {
	return &bucket{places: make(map[string]Place), nextID: 1}
}

// persistedBucket is the on-disk shape of one bucket. Exported fields are
// required for encoding/json; the in-memory bucket stays encapsulated.
type persistedBucket struct {
	Facts  []Fact           `json:"facts"`
	Places map[string]Place `json:"places"`
	NextID int              `json:"next_id"`
}

// persistedStore is the on-disk shape of the whole store.
//
// The flat Facts/Places/NextID trio is the pre-per-server layout. It is only
// read: a file carrying it (and no servers map) has its contents folded into
// the shared bucket, so an operator upgrading the bot does not lose what the
// bot already knew.
type persistedStore struct {
	// Legacy flat layout.
	Facts  []Fact           `json:"facts,omitempty"`
	Places map[string]Place `json:"places,omitempty"`
	NextID int              `json:"next_id,omitempty"`
	// Per-server layout.
	Active  string                     `json:"active,omitempty"`
	Servers map[string]persistedBucket `json:"servers,omitempty"`
}

// Store is a thread-safe, per-server fact + place store with JSON persistence.
type Store struct {
	mu      sync.RWMutex
	path    string
	buckets map[string]*bucket
	active  string
}

// New returns an empty store pointed at the shared bucket, persisted at path.
// Use SetServer to move to a server's own bucket, and Load to populate it.
func New(path string) *Store {
	return &Store{
		path:    path,
		buckets: map[string]*bucket{SharedBucket: newBucket()},
		active:  SharedBucket,
	}
}

// Path returns the persistence file path.
func (s *Store) Path() string {
	return s.path
}

// SetServer points the store at one server's memory bucket. Everything added,
// searched, forgotten, and rendered afterwards belongs to that server.
//
// The key is the host:port the bot is connected to, lower-cased so a dial
// address written "Host:Port" and one written lowercase land in the same
// bucket. The switch is persisted: a restart restores the last server's view
// before the next session re-points it.
func (s *Store) SetServer(server string) {
	key := normalizeServerKey(server)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = key
	s.activeBucket()
	_ = s.saveLocked()
}

// ActiveServer reports the bucket the store currently reads and writes.
func (s *Store) ActiveServer() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// activeBucket returns the live server's bucket, creating it on first use.
// Callers must hold mu.
func (s *Store) activeBucket() *bucket {
	bkt, ok := s.buckets[s.active]
	if !ok {
		bkt = newBucket()
		s.buckets[s.active] = bkt
	}
	return bkt
}

// normalizeServerKey folds a dial address into a bucket key.
func normalizeServerKey(server string) string {
	return strings.ToLower(strings.TrimSpace(server))
}

// Add stores a fact in the active server's bucket. Empty text and over-long
// text are rejected. When the bucket is full the oldest fact is evicted.
// Persistence is best-effort: the fact is kept in memory even when the file
// write fails.
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
	bkt := s.activeBucket()
	f := Fact{
		ID:      bkt.nextID,
		Text:    text,
		Author:  strings.TrimSpace(author),
		SavedAt: time.Now(),
	}
	bkt.nextID++
	bkt.facts = append(bkt.facts, f)
	for len(bkt.facts) > MaxFacts {
		bkt.facts = bkt.facts[1:]
	}
	_ = s.saveLocked()
	return f, nil
}

// Facts returns a copy of the active server's facts, oldest first.
func (s *Store) Facts() []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	facts := s.activeBucket().facts
	out := make([]Fact, len(facts))
	copy(out, facts)
	return out
}

// Search returns the active server's facts whose text contains query
// (case-insensitive). An empty query returns all of them.
func (s *Store) Search(query string) []Fact {
	query = strings.ToLower(strings.TrimSpace(query))
	s.mu.RLock()
	defer s.mu.RUnlock()
	facts := s.activeBucket().facts
	out := make([]Fact, 0, len(facts))
	for _, f := range facts {
		if query == "" || strings.Contains(strings.ToLower(f.Text), query) {
			out = append(out, f)
		}
	}
	return out
}

// Forget removes one fact from the active server's bucket: a numeric query
// matches by fact ID, otherwise the first fact whose text contains the query
// (case-insensitive) is removed.
func (s *Store) Forget(query string) (Fact, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Fact{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bkt := s.activeBucket()
	idx := -1
	if id, err := strconv.Atoi(query); err == nil {
		for i, f := range bkt.facts {
			if f.ID == id {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		lower := strings.ToLower(query)
		for i, f := range bkt.facts {
			if strings.Contains(strings.ToLower(f.Text), lower) {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return Fact{}, false
	}
	removed := bkt.facts[idx]
	bkt.facts = append(bkt.facts[:idx], bkt.facts[idx+1:]...)
	_ = s.saveLocked()
	return removed, true
}

// RememberPlace stores coordinates under a case-insensitive name, in the
// active server's bucket — a "home" on one server is not a home on another.
func (s *Store) RememberPlace(name string, x, y, z float32) (Place, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return Place{}, fmt.Errorf("place name is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bkt := s.activeBucket()
	if _, ok := bkt.places[name]; !ok && len(bkt.places) >= MaxPlaces {
		return Place{}, fmt.Errorf("too many places (max %d)", MaxPlaces)
	}
	p := Place{Name: name, X: x, Y: y, Z: z, SavedAt: time.Now()}
	bkt.places[name] = p
	_ = s.saveLocked()
	return p, nil
}

// Place returns the place stored under name (case-insensitive) in the active
// server's bucket.
func (s *Store) Place(name string) (Place, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.activeBucket().places[name]
	return p, ok
}

// Places returns the active server's places sorted by name.
func (s *Store) Places() []Place {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bkt := s.activeBucket()
	out := make([]Place, 0, len(bkt.places))
	for _, p := range bkt.places {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RemovePlace deletes the named place from the active server's bucket.
func (s *Store) RemovePlace(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	defer s.mu.Unlock()
	bkt := s.activeBucket()
	if _, ok := bkt.places[name]; !ok {
		return false
	}
	delete(bkt.places, name)
	_ = s.saveLocked()
	return true
}

// RenderForPrompt renders a compact memory block for LLM system prompts from
// the active server's bucket. Returns "" when that bucket is empty. At most
// maxFacts facts are included; pass <= 0 for the default.
//
// The server is named in the header so a prompt read out of context states
// whose rules these are — the same bot carries different rules per server.
func (s *Store) RenderForPrompt(maxFacts int) string {
	if maxFacts <= 0 {
		maxFacts = DefaultRenderFacts
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	bkt := s.activeBucket()
	if len(bkt.facts) == 0 && len(bkt.places) == 0 {
		return ""
	}
	head := "[LONG-TERM MEMORY] Things players asked you to remember:"
	if s.active != SharedBucket {
		head = fmt.Sprintf("[LONG-TERM MEMORY — server %s] Things players asked you to remember:", s.active)
	}
	var b strings.Builder
	b.WriteString(head + "\n")
	recent := bkt.facts
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
	if hidden := len(bkt.facts) - len(recent); hidden > 0 {
		fmt.Fprintf(&b, "(+%d more, use recall to list)\n", hidden)
	}
	if len(bkt.places) > 0 {
		names := make([]string, 0, len(bkt.places))
		for _, p := range bkt.places {
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
	servers := make(map[string]persistedBucket, len(s.buckets))
	for key, bkt := range s.buckets {
		servers[key] = persistedBucket{
			Facts:  bkt.facts,
			Places: bkt.places,
			NextID: bkt.nextID,
		}
	}
	data, err := json.MarshalIndent(persistedStore{
		Active:  s.active,
		Servers: servers,
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
//
// Two layouts are accepted. The per-server layout carries a servers map and
// restores every bucket plus the active one. The legacy flat layout — a file
// with facts at the top level — has its contents folded into the shared
// bucket, so nothing an operator dictated before the change is lost; it moves
// to a server's own view the first time that server's bucket is refilled by
// dictation (or by hand, via SetServer + re-remember).
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

	buckets := make(map[string]*bucket)
	for key, pb := range persisted.Servers {
		bkt := newBucket()
		bkt.facts = pb.Facts
		if pb.Places != nil {
			bkt.places = pb.Places
		}
		bkt.nextID = pb.NextID
		buckets[key] = bkt
	}
	if len(buckets) == 0 && (len(persisted.Facts) > 0 || len(persisted.Places) > 0) {
		legacy := newBucket()
		legacy.facts = persisted.Facts
		legacy.places = persisted.Places
		legacy.nextID = persisted.NextID
		buckets[SharedBucket] = legacy
	}
	if _, ok := buckets[SharedBucket]; !ok {
		buckets[SharedBucket] = newBucket()
	}
	for _, bkt := range buckets {
		if bkt.nextID < 1 {
			bkt.nextID = 1
		}
		for _, f := range bkt.facts {
			if f.ID >= bkt.nextID {
				bkt.nextID = f.ID + 1
			}
		}
	}
	s.buckets = buckets
	s.active = SharedBucket
	if _, ok := buckets[persisted.Active]; ok && persisted.Active != "" {
		s.active = persisted.Active
	}
	return nil
}
