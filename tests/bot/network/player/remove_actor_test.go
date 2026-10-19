package player_test

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"bedrock-ai/internal/bot/network/player"
)

// TestRemoveActorClearsRuntimeIDKeyedTracking is the regression guard for the
// unique-ID/runtime-ID mix-up in handleRemoveActor.
//
// AddPlayer records players keyed by RUNTIME id. RemoveActor carried the
// ENTITY UNIQUE id, and the cleanup used that value directly as the map key, so
// the lookup missed: a player who left the view stayed in PlayerUsernames,
// PlayerPositions and PlayerEntityIDs forever, which made FindPlayer and
// GetEntities report phantoms.
func TestRemoveActorClearsRuntimeIDKeyedTracking(t *testing.T) {
	t.Parallel()

	b := newNPCBot()
	const (
		runtimeID = uint64(4242)
		uniqueID  = int64(9001)
	)

	// The spawn mapping is recorded directly. Routing through handleAddActor
	// needs a fully wired Bot (metadata recorder, logger) that this packet does
	// not depend on.
	b.Mu.Lock()
	b.UniqueIDToRuntimeID[uniqueID] = runtimeID
	b.Mu.Unlock()
	if got := b.UniqueIDToRuntimeID[uniqueID]; got != runtimeID {
		t.Fatalf("unique→runtime mapping = %d, want %d", got, runtimeID)
	}

	// AddPlayer is the packet that fills the player maps, keyed by runtime ID.
	b.Mu.Lock()
	b.PlayerUsernames[runtimeID] = "Steve"
	b.PlayerEntityIDs["Steve"] = runtimeID
	b.Mu.Unlock()

	player.HandlePlayerPacket(b, &packet.RemoveActor{EntityUniqueID: uniqueID})

	b.Mu.Lock()
	_, stillListed := b.PlayerUsernames[runtimeID]
	_, stillMapped := b.PlayerEntityIDs["Steve"]
	_, stillPositioned := b.PlayerPositions[runtimeID]
	b.Mu.Unlock()

	if stillListed {
		t.Error("the departed player is still in PlayerUsernames")
	}
	if stillMapped {
		t.Error("the departed player is still in PlayerEntityIDs")
	}
	if stillPositioned {
		t.Error("the departed player is still in PlayerPositions")
	}
}
