package memory_test

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"bedrock-ai/internal/memory"
)

func TestAdd_Validation(t *testing.T) {
	t.Parallel()
	s := memory.New("")
	if _, err := s.Add("   ", "player"); err == nil {
		t.Error("Add with blank text should fail")
	}
	if _, err := s.Add(strings.Repeat("x", memory.MaxFactLen+1), "player"); err == nil {
		t.Error("Add with over-long text should fail")
	}
	f, err := s.Add("base di bukit", "Onyx")
	if err != nil {
		t.Fatalf("Add valid fact failed: %v", err)
	}
	if f.ID != 1 || f.Text != "base di bukit" || f.Author != "Onyx" {
		t.Errorf("unexpected fact: %+v", f)
	}
	if got := len(s.Facts()); got != 1 {
		t.Errorf("Facts() len = %d, want 1", got)
	}
}

func TestSearch_SubstringCaseInsensitive(t *testing.T) {
	t.Parallel()
	s := memory.New("")
	_, _ = s.Add("Diamond di chest rumah", "A")
	_, _ = s.Add("Base di bukit", "B")
	_, _ = s.Add("Portal nether rusak", "C")

	if got := s.Search(""); len(got) != 3 {
		t.Errorf("empty query should return all, got %d", len(got))
	}
	got := s.Search("BUKIT")
	if len(got) != 1 || got[0].Text != "Base di bukit" {
		t.Errorf("case-insensitive search failed: %+v", got)
	}
	if got := s.Search("tidak ada"); len(got) != 0 {
		t.Errorf("expected no matches, got %+v", got)
	}
}

func TestForget_ByIDAndByText(t *testing.T) {
	t.Parallel()
	s := memory.New("")
	_, _ = s.Add("ingat portal", "A")
	second, _ := s.Add("ingat base", "B")

	removed, ok := s.Forget("portal")
	if !ok || removed.Text != "ingat portal" {
		t.Errorf("Forget by text failed: %+v %v", removed, ok)
	}
	removed, ok = s.Forget("2")
	if !ok || removed.ID != second.ID {
		t.Errorf("Forget by ID failed: %+v %v", removed, ok)
	}
	if _, ok := s.Forget("tidak ada"); ok {
		t.Error("Forget unknown query should return false")
	}
	if _, ok := s.Forget(""); ok {
		t.Error("Forget empty query should return false")
	}
}

func TestPlaces_RememberGetRemove(t *testing.T) {
	t.Parallel()
	s := memory.New("")
	p, err := s.RememberPlace("Home", 10, 64, -20)
	if err != nil {
		t.Fatalf("RememberPlace failed: %v", err)
	}
	if p.Name != "home" {
		t.Errorf("place name should be lowercased, got %q", p.Name)
	}
	got, ok := s.Place("HOME")
	if !ok || got.X != 10 || got.Y != 64 || got.Z != -20 {
		t.Errorf("Place lookup failed: %+v %v", got, ok)
	}
	if _, err := s.RememberPlace("  ", 0, 0, 0); err == nil {
		t.Error("RememberPlace with blank name should fail")
	}
	if !s.RemovePlace("home") {
		t.Error("RemovePlace should return true")
	}
	if s.RemovePlace("home") {
		t.Error("RemovePlace twice should return false")
	}
}

func TestPersistence_SaveLoadRoundtrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sub", "mem.json")
	s := memory.New(path)
	_, _ = s.Add("ingat iron", "A")
	_, _ = s.RememberPlace("home", 1, 2, 3)
	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := memory.New(path)
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	facts := loaded.Facts()
	if len(facts) != 1 || facts[0].Text != "ingat iron" {
		t.Errorf("facts mismatch after load: %+v", facts)
	}
	place, ok := loaded.Place("home")
	if !ok || place.X != 1 {
		t.Errorf("place mismatch after load: %+v %v", place, ok)
	}
	// IDs must keep increasing after load, not restart at 1.
	next, err := loaded.Add("kedua", "B")
	if err != nil {
		t.Fatalf("Add after load failed: %v", err)
	}
	if next.ID != facts[0].ID+1 {
		t.Errorf("ID after load = %d, want %d", next.ID, facts[0].ID+1)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	t.Parallel()
	s := memory.New(filepath.Join(t.TempDir(), "nope.json"))
	if err := s.Load(); err == nil {
		t.Error("Load of missing file should return an error")
	}
	if got := len(s.Facts()); got != 0 {
		t.Errorf("store should stay empty, got %d facts", got)
	}
}

func TestRenderForPrompt(t *testing.T) {
	t.Parallel()
	s := memory.New("")
	if got := s.RenderForPrompt(10); got != "" {
		t.Errorf("empty store should render empty, got %q", got)
	}
	_, _ = s.Add("suka kucing", "A")
	_, _ = s.RememberPlace("home", 10, 64, -20)
	rendered := s.RenderForPrompt(10)
	if !strings.Contains(rendered, "suka kucing") {
		t.Errorf("render missing fact: %q", rendered)
	}
	if !strings.Contains(rendered, "home (X:10 Y:64 Z:-20)") {
		t.Errorf("render missing place: %q", rendered)
	}
	// Truncation keeps newest and notes the hidden count.
	for i := 0; i < 15; i++ {
		_, _ = s.Add("filler", "A")
	}
	rendered = s.RenderForPrompt(10)
	if strings.Contains(rendered, "suka kucing") {
		t.Errorf("oldest fact should be truncated: %q", rendered)
	}
	if !strings.Contains(rendered, "more, use recall") {
		t.Errorf("render should note hidden facts: %q", rendered)
	}
}

func TestStore_ConcurrentUse(t *testing.T) {
	t.Parallel()
	s := memory.New("")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Add("concurrent", "A")
			_ = s.Search("concurrent")
			_ = s.Facts()
			_, _ = s.RememberPlace("spot", 1, 2, 3)
			_, _ = s.Place("spot")
			_ = s.Places()
			_ = s.RenderForPrompt(5)
		}()
	}
	wg.Wait()
	if got := len(s.Facts()); got != 8 {
		t.Errorf("Facts() len = %d, want 8", got)
	}
}
