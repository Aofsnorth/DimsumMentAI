package world_test

import (
	"testing"

	"bedrock-ai/internal/bot/world"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
)

func TestNetworkRuntimeIDUsesServerSelectedFormat(t *testing.T) {
	t.Parallel()

	rid, ok := chunk.StateToRuntimeID("minecraft:stone", nil)
	if !ok {
		t.Fatal("stone runtime ID is unavailable")
	}
	cache := world.NewWorldCache(0, cube.Range{-64, 319}, nil)

	got, ok := cache.NetworkRuntimeID(rid)
	if !ok || got != rid {
		t.Fatalf("numeric network ID = %d, %v; want %d, true", got, ok, rid)
	}

	cache.SetUseBlockNetworkIDHashes(true)
	hash, ok := cache.NetworkRuntimeID(rid)
	if !ok {
		t.Fatal("hashed network ID is unavailable")
	}
	if hash == rid {
		t.Fatalf("hashed network ID unexpectedly equals local runtime ID %d", rid)
	}
	if translated := cache.TranslateRuntimeID(hash); translated != rid {
		t.Fatalf("TranslateRuntimeID(%d) = %d, want %d", hash, translated, rid)
	}
}
