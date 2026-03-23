package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// fileStore implements MemoryStore using file-based JSON storage with caching
type fileStore struct {
	mu      sync.RWMutex
	baseDir string
	cache   map[string]*PlayerMemory
}

func newFileStore(baseDir string) *fileStore {
	return &fileStore{
		baseDir: baseDir,
		cache:   make(map[string]*PlayerMemory),
	}
}

func (s *fileStore) filePath(playerID string) string {
	return filepath.Join(s.baseDir, playerID+".json")
}

func (s *fileStore) Save(playerID string, mem *PlayerMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(mem, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.filePath(playerID) + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, s.filePath(playerID)); err != nil {
		return err
	}

	// Update cache with a copy
	memCopy := &PlayerMemory{
		PlayerID:          mem.PlayerID,
		Conversation:      make([]ConversationMessage, len(mem.Conversation)),
		WorldObservations: make([]WorldObservation, len(mem.WorldObservations)),
	}
	copy(memCopy.Conversation, mem.Conversation)
	copy(memCopy.WorldObservations, mem.WorldObservations)
	for i, obs := range mem.WorldObservations {
		structuredCopy := make(map[string]interface{})
		for k, v := range obs.Structured {
			structuredCopy[k] = v
		}
		memCopy.WorldObservations[i].Structured = structuredCopy
	}
	s.cache[playerID] = memCopy

	return nil
}

func (s *fileStore) Load(playerID string) (*PlayerMemory, error) {
	s.mu.RLock()
	if mem, exists := s.cache[playerID]; exists {
		s.mu.RUnlock()
		// Return a copy
		memCopy := &PlayerMemory{
			PlayerID:          mem.PlayerID,
			Conversation:      make([]ConversationMessage, len(mem.Conversation)),
			WorldObservations: make([]WorldObservation, len(mem.WorldObservations)),
		}
		copy(memCopy.Conversation, mem.Conversation)
		copy(memCopy.WorldObservations, mem.WorldObservations)
		for i, obs := range mem.WorldObservations {
			structuredCopy := make(map[string]interface{})
			for k, v := range obs.Structured {
				structuredCopy[k] = v
			}
			memCopy.WorldObservations[i].Structured = structuredCopy
		}
		return memCopy, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-check cache after acquiring write lock
	if mem, exists := s.cache[playerID]; exists {
		memCopy := &PlayerMemory{
			PlayerID:          mem.PlayerID,
			Conversation:      make([]ConversationMessage, len(mem.Conversation)),
			WorldObservations: make([]WorldObservation, len(mem.WorldObservations)),
		}
		copy(memCopy.Conversation, mem.Conversation)
		copy(memCopy.WorldObservations, mem.WorldObservations)
		for i, obs := range mem.WorldObservations {
			structuredCopy := make(map[string]interface{})
			for k, v := range obs.Structured {
				structuredCopy[k] = v
			}
			memCopy.WorldObservations[i].Structured = structuredCopy
		}
		return memCopy, nil
	}

	path := s.filePath(playerID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &PlayerMemory{PlayerID: playerID}, nil
		}
		return nil, err
	}

	var mem PlayerMemory
	if err := json.Unmarshal(data, &mem); err != nil {
		return nil, err
	}

	// Populate cache with a copy
	memCopy := &PlayerMemory{
		PlayerID:          mem.PlayerID,
		Conversation:      make([]ConversationMessage, len(mem.Conversation)),
		WorldObservations: make([]WorldObservation, len(mem.WorldObservations)),
	}
	copy(memCopy.Conversation, mem.Conversation)
	copy(memCopy.WorldObservations, mem.WorldObservations)
	for i, obs := range mem.WorldObservations {
		structuredCopy := make(map[string]interface{})
		for k, v := range obs.Structured {
			structuredCopy[k] = v
		}
		memCopy.WorldObservations[i].Structured = structuredCopy
	}
	s.cache[playerID] = memCopy

	return &mem, nil
}

func (s *fileStore) AppendConversation(playerID string, msg ConversationMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mem, exists := s.cache[playerID]
	if !exists {
		// Load from file
		path := s.filePath(playerID)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				mem = &PlayerMemory{
					PlayerID:          playerID,
					Conversation:      []ConversationMessage{},
					WorldObservations: []WorldObservation{},
				}
			} else {
				return err
			}
		} else {
			if err := json.Unmarshal(data, &mem); err != nil {
				return err
			}
		}
	}

	mem.Conversation = append(mem.Conversation, msg)

	return s.saveLocked(mem)
}

func (s *fileStore) AppendWorldObservation(playerID string, obs WorldObservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mem, exists := s.cache[playerID]
	if !exists {
		path := s.filePath(playerID)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				mem = &PlayerMemory{
					PlayerID:          playerID,
					Conversation:      []ConversationMessage{},
					WorldObservations: []WorldObservation{},
				}
			} else {
				return err
			}
		} else {
			if err := json.Unmarshal(data, &mem); err != nil {
				return err
			}
		}
	}

	mem.WorldObservations = append(mem.WorldObservations, obs)

	return s.saveLocked(mem)
}

func (s *fileStore) Search(playerID, query string, limit int) ([]string, error) {
	s.mu.RLock()
	mem, exists := s.cache[playerID]
	if !exists {
		s.mu.RUnlock()
		// Load from file
		var err error
		mem, err = s.Load(playerID)
		if err != nil {
			return nil, err
		}
	} else {
		s.mu.RUnlock()
	}

	var results []string
	lowerQuery := strings.ToLower(query)

	for _, msg := range mem.Conversation {
		if strings.Contains(strings.ToLower(msg.Content), lowerQuery) {
			results = append(results, msg.Content)
			if limit > 0 && len(results) >= limit {
				return results, nil
			}
		}
	}

	for _, obs := range mem.WorldObservations {
		if strings.Contains(strings.ToLower(obs.Summary), lowerQuery) {
			results = append(results, obs.Summary)
			if limit > 0 && len(results) >= limit {
				return results, nil
			}
		}
	}

	return results, nil
}

func (s *fileStore) TrimConversation(playerID string, keep int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mem, exists := s.cache[playerID]
	if !exists {
		return nil
	}

	if keep < 0 {
		keep = 0
	}

	if len(mem.Conversation) <= keep {
		return nil
	}

	mem.Conversation = mem.Conversation[len(mem.Conversation)-keep:]

	return s.saveLocked(mem)
}

func (s *fileStore) saveLocked(mem *PlayerMemory) error {
	data, err := json.MarshalIndent(mem, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.filePath(mem.PlayerID) + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, s.filePath(mem.PlayerID)); err != nil {
		return err
	}

	// Update cache
	memCopy := &PlayerMemory{
		PlayerID:          mem.PlayerID,
		Conversation:      make([]ConversationMessage, len(mem.Conversation)),
		WorldObservations: make([]WorldObservation, len(mem.WorldObservations)),
	}
	copy(memCopy.Conversation, mem.Conversation)
	copy(memCopy.WorldObservations, mem.WorldObservations)
	for i, obs := range mem.WorldObservations {
		structuredCopy := make(map[string]interface{})
		for k, v := range obs.Structured {
			structuredCopy[k] = v
		}
		memCopy.WorldObservations[i].Structured = structuredCopy
	}
	s.cache[mem.PlayerID] = memCopy

	return nil
}
