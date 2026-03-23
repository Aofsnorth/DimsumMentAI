package memory

import (
	"strings"
	"sync"
)

// inMemoryStore implements MemoryStore using in-memory map storage
type inMemoryStore struct {
	mu      sync.RWMutex
	players map[string]*PlayerMemory
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{
		players: make(map[string]*PlayerMemory),
	}
}

func (s *inMemoryStore) Save(playerID string, mem *PlayerMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create a deep copy to store
	copyMem := &PlayerMemory{
		PlayerID:          mem.PlayerID,
		Conversation:      make([]ConversationMessage, len(mem.Conversation)),
		WorldObservations: make([]WorldObservation, len(mem.WorldObservations)),
	}
	copy(copyMem.Conversation, mem.Conversation)
	copy(copyMem.WorldObservations, mem.WorldObservations)
	for i, obs := range mem.WorldObservations {
		structuredCopy := make(map[string]interface{})
		for k, v := range obs.Structured {
			structuredCopy[k] = v
		}
		copyMem.WorldObservations[i].Structured = structuredCopy
	}

	s.players[playerID] = copyMem
	return nil
}

func (s *inMemoryStore) Load(playerID string) (*PlayerMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mem, exists := s.players[playerID]
	if !exists {
		return &PlayerMemory{PlayerID: playerID}, nil
	}

	// Return a copy
	copyMem := &PlayerMemory{
		PlayerID:          mem.PlayerID,
		Conversation:      make([]ConversationMessage, len(mem.Conversation)),
		WorldObservations: make([]WorldObservation, len(mem.WorldObservations)),
	}
	copy(copyMem.Conversation, mem.Conversation)
	copy(copyMem.WorldObservations, mem.WorldObservations)
	for i, obs := range mem.WorldObservations {
		structuredCopy := make(map[string]interface{})
		for k, v := range obs.Structured {
			structuredCopy[k] = v
		}
		copyMem.WorldObservations[i].Structured = structuredCopy
	}

	return copyMem, nil
}

func (s *inMemoryStore) AppendConversation(playerID string, msg ConversationMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mem, exists := s.players[playerID]
	if !exists {
		mem = &PlayerMemory{
			PlayerID:          playerID,
			Conversation:      []ConversationMessage{},
			WorldObservations: []WorldObservation{},
		}
		s.players[playerID] = mem
	}

	mem.Conversation = append(mem.Conversation, msg)
	return nil
}

func (s *inMemoryStore) AppendWorldObservation(playerID string, obs WorldObservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mem, exists := s.players[playerID]
	if !exists {
		mem = &PlayerMemory{
			PlayerID:          playerID,
			Conversation:      []ConversationMessage{},
			WorldObservations: []WorldObservation{},
		}
		s.players[playerID] = mem
	}

	mem.WorldObservations = append(mem.WorldObservations, obs)
	return nil
}

func (s *inMemoryStore) Search(playerID, query string, limit int) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []string
	lowerQuery := strings.ToLower(query)

	mem, exists := s.players[playerID]
	if !exists {
		return results, nil
	}

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

func (s *inMemoryStore) TrimConversation(playerID string, keep int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mem, exists := s.players[playerID]
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
	return nil
}
