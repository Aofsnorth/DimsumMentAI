package world

import (
	"testing"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
)

func TestSetBlockRIDUpdatesCachedBlock(t *testing.T) {
	airRID, ok := chunk.StateToRuntimeID("minecraft:air", nil)
	if !ok {
		t.Fatal("missing air runtime id")
	}
	dirtRID, ok := chunk.StateToRuntimeID("minecraft:dirt", nil)
	if !ok {
		t.Fatal("missing dirt runtime id")
	}

	cache := NewWorldCache(airRID, cube.Range{-64, 319}, nil)

	cache.SetBlockRID(10, 64, -5, dirtRID)

	if got, loaded := cache.GetBlockRID(10, 64, -5); !loaded || got != dirtRID {
		t.Fatalf("GetBlockRID() = (%d, %v), want (%d, true)", got, loaded, dirtRID)
	}

	cache.SetBlockRID(10, 64, -5, airRID)

	if solid, loaded := cache.IsBlockSolid(10, 64, -5); !loaded || solid {
		t.Fatalf("IsBlockSolid() = (%v, %v), want (false, true)", solid, loaded)
	}
}
