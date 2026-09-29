package action

import (
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/world"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"testing"
)

// Fast regression: semantic navigation cannot turn received chunks into xray.
func TestNamedNavigationRequiresVisibleTarget(t *testing.T) {
	t.Parallel()
	stone, ok := chunk.StateToRuntimeID("minecraft:stone", nil)
	if !ok {
		t.Fatal("stone runtime ID unavailable")
	}
	b := &bot.Bot{WorldCache: world.NewWorldCache(0, cube.Range{-64, 319}, nil)}
	b.WorldCache.SetBlockRID(0, 1, 5, stone)
	lookup := botBlockLookup(b)
	if _, ok := lookup(0, 1, 5); !ok {
		t.Fatal("visible target unavailable")
	}
	b.WorldCache.SetBlockRID(0, 1, 2, stone)
	if _, ok := lookup(0, 1, 5); ok {
		t.Fatal("hidden target leaked")
	}
	b.WorldCache.SetBlockRID(0, 1, -5, stone)
	if _, ok := lookup(0, 1, -5); ok {
		t.Fatal("rear target leaked")
	}
	if _, ok := lookup(40, 1, 40); ok {
		t.Fatal("unknown target leaked")
	}
}
