package movement

import (
	"context"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// ExecuteScaffoldAction handles breaking blocking blocks or placing blocks to advance the path.
func ExecuteScaffoldAction(b *bot.Bot, node pathfinder.Node) {
	b.Logger.Info("Executing scaffold/mine action", "action", node.Action, "node", node)
	defer func() {
		b.Mu.Lock()
		b.ScaffoldingActive = false
		b.Mu.Unlock()
		b.Logger.Info("Scaffold action complete, resuming movement")
	}()

	if node.Action == "mine" {
		bx, by, bz := node.X, node.Y, node.Z
		mineBlockIfSolid(b, bx, by, bz)
		mineBlockIfSolid(b, bx, by+1, bz)
		time.Sleep(200 * time.Millisecond)
	} else if node.Action == "place" {
		b.Mu.Lock()
		botY := b.Pos.Y()
		b.Mu.Unlock()

		var placePos protocol.BlockPos
		if float32(node.Y) > botY+0.5 {
			placePos = protocol.BlockPos{
				node.X,
				int32(math.Floor(float64(botY))) - 1,
				node.Z,
			}
			placeScaffoldBlock(b, placePos, true)
		} else {
			placePos = protocol.BlockPos{
				node.X,
				node.Y - 1,
				node.Z,
			}
			placeScaffoldBlock(b, placePos, false)
		}
	}
}

func mineBlockIfSolid(b *bot.Bot, x, y, z int32) {
	if b.WorldCache == nil {
		return
	}
	isSolid, loaded := b.WorldCache.IsBlockSolid(x, y, z)
	if !loaded || !isSolid {
		return
	}

	b.Logger.Info("Mining blocking block", "x", x, "y", y, "z", z)
	targetCenter := mgl32.Vec3{float32(x) + 0.5, float32(y) + 0.5, float32(z) + 0.5}
	b.LookAt(targetCenter)
	time.Sleep(150 * time.Millisecond)

	blockPos := protocol.BlockPos{x, y, z}
	_ = b.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: b.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   blockPos,
		BlockFace:       1,
	})

	breakTime := 800 * time.Millisecond
	name, ok := b.GetBlockName(x, y, z)
	if ok && (strings.Contains(name, "stone") || strings.Contains(name, "ore")) {
		breakTime = 1500 * time.Millisecond
	}

	elapsed := time.Duration(0)
	swingInterval := 250 * time.Millisecond
	for elapsed < breakTime {
		_ = b.WritePacket(&packet.Animate{
			ActionType:      packet.AnimateActionSwingArm,
			EntityRuntimeID: b.GetEntityRuntimeID(),
		})
		b.LookAt(targetCenter)
		time.Sleep(swingInterval)
		elapsed += swingInterval
	}

	_ = b.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: b.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionCrackBreak,
		BlockPosition:   blockPos,
		BlockFace:       1,
	})
	_ = b.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: b.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionPredictDestroyBlock,
		BlockPosition:   blockPos,
		BlockFace:       1,
	})

	if b.WorldModel != nil {
		b.WorldModel.SetSolid(x, y, z, false)
	}
	time.Sleep(200 * time.Millisecond)

	if b.Gatherer != nil {
		b.Gatherer.CollectAllDrops(context.Background(), 5.0)
	}
}

func placeScaffoldBlock(b *bot.Bot, refPos protocol.BlockPos, isTower bool) {
	slot, item, ok := b.Gatherer.FindScaffoldItem()
	if !ok {
		b.Logger.Warn("No blocks to place! Gathering dirt blocks first")
		b.ReportActionStatus("", event.ActionStatus{Action: "gather", Item: "dirt", Success: false, Error: "gak punya block buat lewat"})

		b.Mu.Lock()
		b.CurrentPath = nil
		b.MovementState = "idle"
		b.Mu.Unlock()

		b.Gatherer.GatherBlock(context.Background(), "dirt", 5)
		return
	}

	if err := b.EquipItem(slot); err != nil {
		return
	}

	b.Logger.Info("Placing block for path", "pos", refPos, "item", item)

	if isTower {
		b.Mu.Lock()
		b.VelY = 0.42
		b.IsGrounded = false
		b.Mu.Unlock()
		time.Sleep(150 * time.Millisecond)

		b.Mu.Lock()
		botPos := b.Pos
		b.Mu.Unlock()
		b.LookAt(botPos.Add(mgl32.Vec3{0, -2.0, 0}))
	} else {
		b.LookAt(mgl32.Vec3{float32(refPos.X()) + 0.5, float32(refPos.Y()) + 0.5, float32(refPos.Z()) + 0.5})
	}

	time.Sleep(100 * time.Millisecond)

	b.Mu.Lock()
	curPos := b.Pos
	heldSlot := b.HeldSlot
	b.Mu.Unlock()

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   refPos,
			BlockFace:       1,
			HotBarSlot:      int32(heldSlot),
			HeldItem:        protocol.ItemInstance{Stack: item},
			Position:        curPos,
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}

	_ = b.WritePacket(tx)

	if b.WorldModel != nil {
		b.WorldModel.SetSolid(refPos.X(), refPos.Y()+1, refPos.Z(), true)
	}

	time.Sleep(200 * time.Millisecond)
}
