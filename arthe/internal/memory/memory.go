package memory

import "errors"

// ErrStoreNotFound is returned when a store type is not recognized
var ErrStoreNotFound = errors.New("unknown store type")

// ConversationMessage is a single message in a player's conversation history
type ConversationMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

// WorldObservation is LLM2's structured world scan data
type WorldObservation struct {
	Timestamp  int64                 `json:"timestamp"`
	Tick       int64                 `json:"tick"`
	Summary    string                `json:"summary"`    // LLM-generated text summary
	Structured map[string]interface{} `json:"structured"` // raw world data
}

// PlayerMemory holds all memory for a single player
type PlayerMemory struct {
	PlayerID          string                 `json:"player_id"`
	Conversation      []ConversationMessage  `json:"conversation"`
	WorldObservations []WorldObservation      `json:"world_observations"`
}

// MemoryStore defines the interface for memory storage implementations
type MemoryStore interface {
	// Save persists memory for a player
	Save(playerID string, mem *PlayerMemory) error
	// Load retrieves memory for a player (returns empty if none)
	Load(playerID string) (*PlayerMemory, error)
	// AppendConversation adds a message to conversation history
	AppendConversation(playerID string, msg ConversationMessage) error
	// AppendWorldObservation adds a world observation
	AppendWorldObservation(playerID string, obs WorldObservation) error
	// Search searches both conversation and world observations
	// Returns combined results matching the query, up to limit results
	Search(playerID, query string, limit int) ([]string, error)
	// TrimConversation keeps only the last N messages
	TrimConversation(playerID string, keep int) error
}

// NewMemoryStore creates a memory store based on the storage type
func NewMemoryStore(storage, filePath string) (MemoryStore, error) {
	switch storage {
	case "memory":
		return newInMemoryStore(), nil
	case "file":
		return newFileStore(filePath), nil
	case "both":
		memStore := newInMemoryStore()
		fileStore := newFileStore(filePath)
		return &bothStore{
			memStore:  memStore,
			fileStore: fileStore,
		}, nil
	default:
		return nil, ErrStoreNotFound
	}
}

// bothStore wraps two stores, writing to both and reading from file store
type bothStore struct {
	memStore  MemoryStore
	fileStore MemoryStore
}

func (s *bothStore) Save(playerID string, mem *PlayerMemory) error {
	if err := s.memStore.Save(playerID, mem); err != nil {
		return err
	}
	return s.fileStore.Save(playerID, mem)
}

func (s *bothStore) Load(playerID string) (*PlayerMemory, error) {
	return s.fileStore.Load(playerID)
}

func (s *bothStore) AppendConversation(playerID string, msg ConversationMessage) error {
	if err := s.memStore.AppendConversation(playerID, msg); err != nil {
		return err
	}
	return s.fileStore.AppendConversation(playerID, msg)
}

func (s *bothStore) AppendWorldObservation(playerID string, obs WorldObservation) error {
	if err := s.memStore.AppendWorldObservation(playerID, obs); err != nil {
		return err
	}
	return s.fileStore.AppendWorldObservation(playerID, obs)
}

func (s *bothStore) Search(playerID, query string, limit int) ([]string, error) {
	return s.fileStore.Search(playerID, query, limit)
}

func (s *bothStore) TrimConversation(playerID string, keep int) error {
	if err := s.memStore.TrimConversation(playerID, keep); err != nil {
		return err
	}
	return s.fileStore.TrimConversation(playerID, keep)
}
