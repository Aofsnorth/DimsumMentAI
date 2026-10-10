package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"bedrock-ai/internal/memory"
)

// Memories are per server. A rule dictated on one hub is a rule that hub's
// players agreed to; the next server the bot joins did not agree to it, and a
// store that answers across servers teaches the bot to follow rules nobody
// gave it there. These tests pin the bucketing: the switch, the isolation in
// both directions, the persistence of every bucket at once, and the old flat
// file still loading.

// TestMemoriesStayPerServer is the isolation itself. Adding on A, switching
// to B, and finding nothing is the whole feature; switching back by a
// differently-cased address must find the same bucket again, because dial
// addresses are typed by hand more often than they are copied.
func TestMemoriesStayPerServer(t *testing.T) {
	t.Parallel()

	s := memory.New("")
	if _, err := s.Add("rules lobby: jalan maju, klik npc depan", "Synterix"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	s.SetServer("Survival.Hub:19132")
	if got := s.Facts(); len(got) != 0 {
		t.Fatalf("another server's bucket leaked facts: %+v", got)
	}
	if _, err := s.Add("rules survival: jangan grief", "Synterix"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Same bucket, different case and spacing.
	s.SetServer("  survival.hub:19132 ")
	got := s.Facts()
	if len(got) != 1 || got[0].Text != "rules survival: jangan grief" {
		t.Fatalf("the survival bucket did not round-trip: %+v", got)
	}
}

// TestFactIDsArePerServer keeps a Forget by ID from reaching across buckets:
// both servers number from one, so "1" always means "on the server I am on".
func TestFactIDsArePerServer(t *testing.T) {
	t.Parallel()

	s := memory.New("")
	s.SetServer("a:1")
	first, _ := s.Add("milik A", "A")
	s.SetServer("b:1")
	second, _ := s.Add("milik B", "B")
	if first.ID != second.ID {
		t.Fatalf("IDs crossed buckets: A=%d B=%d", first.ID, second.ID)
	}

	removed, ok := s.Forget("1")
	if !ok || removed.Text != "milik B" {
		t.Fatalf("Forget(\"1\") removed %+v, want B's own fact", removed)
	}
	if got := len(s.Facts()); got != 0 {
		t.Fatalf("B's bucket still holds %d facts", got)
	}
	s.SetServer("a:1")
	if got := s.Facts(); len(got) != 1 || got[0].Text != "milik A" {
		t.Fatalf("A's fact did not survive a Forget aimed at B: %+v", got)
	}
}

// TestPlacesArePerServer: "home" on the hub is a bed next to the portal queue;
// "home" on survival is a base. One walking to the other is the bot standing
// in the wrong world insisting it arrived.
func TestPlacesArePerServer(t *testing.T) {
	t.Parallel()

	s := memory.New("")
	s.SetServer("hub:19132")
	if _, err := s.RememberPlace("home", 1, 64, 2); err != nil {
		t.Fatalf("RememberPlace failed: %v", err)
	}
	s.SetServer("survival:19132")
	if _, ok := s.Place("home"); ok {
		t.Fatal("the hub's home answered on survival")
	}
	if _, err := s.RememberPlace("home", 100, 64, -50); err != nil {
		t.Fatalf("RememberPlace failed: %v", err)
	}
	s.SetServer("hub:19132")
	got, ok := s.Place("home")
	if !ok || got.X != 1 {
		t.Fatalf("the hub's home was overwritten by survival's: %+v", got)
	}
}

// TestEveryServerPersistsAtOnce is the restart. Both buckets are in one file;
// loading must restore both and land on the last active server, so a bot that
// died on survival wakes up still reading survival's rules.
func TestEveryServerPersistsAtOnce(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mem.json")
	s := memory.New(path)
	s.SetServer("hub:19132")
	_, _ = s.Add("aturan hub", "A")
	s.SetServer("survival:19132")
	_, _ = s.Add("aturan survival", "A")
	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := memory.New(path)
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got := loaded.ActiveServer(); got != "survival:19132" {
		t.Fatalf("active server after load = %q, want survival", got)
	}
	got := loaded.Facts()
	if len(got) != 1 || got[0].Text != "aturan survival" {
		t.Fatalf("survival's facts did not survive: %+v", got)
	}
	loaded.SetServer("hub:19132")
	if got := loaded.Facts(); len(got) != 1 || got[0].Text != "aturan hub" {
		t.Fatalf("the hub's facts did not survive: %+v", got)
	}
}

// TestLegacyFlatFileFoldsIntoSharedBucket keeps an operator's upgrade from
// being amnesia. The pre-per-server file carried facts at the top level; a
// store that refused it would silently drop everything the bot already knew.
func TestLegacyFlatFileFoldsIntoSharedBucket(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy.json")
	legacy := map[string]any{
		"facts": []map[string]any{
			{"id": 1, "text": "aturan lama", "author": "Synterix", "saved_at": "2026-01-01T00:00:00Z"},
		},
		"places":  map[string]any{},
		"next_id": 2,
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	s := memory.New(path)
	if err := s.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	got := s.Facts()
	if len(got) != 1 || got[0].Text != "aturan lama" {
		t.Fatalf("the legacy facts were dropped: %+v", got)
	}
	// Numbering continues where the old file left off, so a Forget by ID
	// reaches the fact the player was shown.
	next, err := s.Add("baru", "B")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if next.ID != 2 {
		t.Errorf("next ID = %d, want 2 (continuing the legacy numbering)", next.ID)
	}
}
