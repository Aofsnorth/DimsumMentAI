package player_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/network/player"
)

func newNPCBot() *bot.Bot {
	return &bot.Bot{
		Name:                "Luna",
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		PlayerTracker:       bot.NewPlayerTracker(),
		UniqueIDToRuntimeID: make(map[int64]uint64),
		Actors:              make(map[uint64]*entity.Info),
	}
}

// Geyser spawns a skinned NPC with AddPlayer and then removes its tab-list
// entry. The NPC is still in the world and must stay clickable.
func TestPlayerListRemoveKeepsServerNPCTracked(t *testing.T) {
	t.Parallel()
	b := newNPCBot()
	id := uuid.New()

	player.HandlePlayerPacket(b, &packet.PlayerList{Entries: []protocol.PlayerListEntry{
		{ActionType: protocol.PlayerListActionAdd, UUID: id, Username: "npc"},
	}})
	player.HandlePlayerPacket(b, &packet.AddPlayer{
		UUID: id, Username: "npc", EntityRuntimeID: 77, Position: mgl32.Vec3{1, 97, 2},
	})
	player.HandlePlayerPacket(b, &packet.PlayerList{Entries: []protocol.PlayerListEntry{
		{ActionType: protocol.PlayerListActionRemove, UUID: id},
	}})

	if _, ok := b.GetPlayerList()[77]; !ok {
		t.Fatal("server NPC was forgotten when its tab-list entry was removed")
	}
	if !b.IsServerNPC(77) {
		t.Fatal("NPC with a removed tab-list entry should be flagged as a server NPC")
	}

	player.HandlePlayerPacket(b, &packet.RemoveActor{EntityUniqueID: 77})
	if _, ok := b.GetPlayerList()[77]; ok {
		t.Fatal("RemoveActor must end tracking of the NPC")
	}
}

func TestRealPlayerIsNotServerNPC(t *testing.T) {
	t.Parallel()
	b := newNPCBot()
	id := uuid.New()
	player.HandlePlayerPacket(b, &packet.PlayerList{Entries: []protocol.PlayerListEntry{
		{ActionType: protocol.PlayerListActionAdd, UUID: id, Username: "Steve"},
	}})
	player.HandlePlayerPacket(b, &packet.AddPlayer{UUID: id, Username: "Steve", EntityRuntimeID: 5})
	if b.IsServerNPC(5) {
		t.Fatal("a listed player must not be treated as an NPC")
	}
}

// Several NPCs commonly share one username; none may shadow another.
func TestPlayersWithSameUsernameAreAllListed(t *testing.T) {
	t.Parallel()
	b := newNPCBot()
	for _, rid := range []uint64{10, 11, 12} {
		player.HandlePlayerPacket(b, &packet.AddPlayer{
			UUID: uuid.New(), Username: "", EntityRuntimeID: rid, Position: mgl32.Vec3{float32(rid), 97, 0},
		})
	}
	if got := len(b.GetPlayerList()); got != 3 {
		t.Fatalf("GetPlayerList returned %d players, want 3", got)
	}
}
