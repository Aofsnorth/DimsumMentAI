package gathering

import (
	"bedrock-ai/internal/safecast"
	"context"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type Scaffolder struct {
	rg     *ResourceGatherer
	logger *slog.Logger
}

func NewScaffolder(rg *ResourceGatherer, logger *slog.Logger) *Scaffolder {
	return &Scaffolder{
		rg:     rg,
		logger: logger,
	}
}

func (s *Scaffolder) FindScaffoldItem() (uint32, protocol.ItemStack, bool) {
	inv := s.rg.bot.GetInventorySlots()
	names := s.rg.bot.GetItemNames()

	priority := []string{"dirt", "cobblestone", "stone", "netherrack", "sand", "gravel", "clay", "mud"}
	for _, p := range priority {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := names[item.NetworkID]
			if strings.Contains(strings.ToLower(name), p) {
				return slot, item, true
			}
		}
	}
	return 0, protocol.ItemStack{}, false
}

// scaffoldStockTarget is how many blocks the bot tries to have on hand before
// it starts towering. Small on purpose: mining is a detour, and the tower only
// needs a few blocks to finish the trunk.
const scaffoldStockTarget = 6

// scaffoldStockBlocks are the blocks mined when the bot has nothing to tower
// with, in preference order.
var scaffoldStockBlocks = []string{"dirt", "cobblestone"}

func (s *Scaffolder) TowerUpTo(ctx context.Context, targetY float32) {
	bot := s.rg.bot
	s.logger.Debug("Towering up", "target_y", targetY)
	stocked := false

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		curPos := bot.GetCoords()
		if curPos.Y() >= targetY-0.5 {
			break
		}

		slot, item, ok := s.FindScaffoldItem()
		if !ok {
			// Nothing to build with. Mine a small stock before giving up: the
			// alternative was standing at the foot of a tree staring at logs that
			// are out of reach, then reporting the whole gather as failed. Only
			// ever stock once per tower, so a world without dirt cannot spin here.
			if !stocked {
				stocked = true
				s.ensureScaffoldStock(ctx)
				continue
			}
			s.logger.Warn("No scaffold items found, aborting tower up")
			break
		}

		if err := bot.EquipItem(slot); err != nil {
			break
		}

		bot.LookAt(curPos.Add(mgl32.Vec3{0, -2.0, 0}))
		time.Sleep(50 * time.Millisecond)

		refPos := protocol.BlockPos{
			int32(math.Floor(float64(curPos.X()))),
			int32(math.Floor(float64(curPos.Y()))) - 1,
			int32(math.Floor(float64(curPos.Z()))),
		}

		tx := &packet.InventoryTransaction{
			TransactionData: &protocol.UseItemTransactionData{
				ActionType:      protocol.UseItemActionClickBlock,
				BlockPosition:   refPos,
				BlockFace:       1,
				HotBarSlot:      safecast.To[int32](bot.GetHeldItemSlot()),
				HeldItem:        protocol.ItemInstance{Stack: item},
				Position:        curPos.Add(mgl32.Vec3{0, 1.0, 0}),
				ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
			},
		}

		_ = bot.WritePacket(tx)

		world := bot.GetLocalWorldModel()
		world.SetSolid(refPos.X(), refPos.Y()+1, refPos.Z(), true)

		time.Sleep(200 * time.Millisecond)
	}
}

// ensureScaffoldStock mines a few blocks to tower with. It stops as soon as
// anything usable is on hand, so a bot that already carries cobblestone never
// goes mining for dirt.
func (s *Scaffolder) ensureScaffoldStock(ctx context.Context) {
	for _, blockName := range scaffoldStockBlocks {
		if _, _, ok := s.FindScaffoldItem(); ok {
			return
		}
		s.logger.Info("No scaffold blocks on hand, mining a small stock",
			"block", blockName, "target", scaffoldStockTarget)
		// Quiet on purpose: this is an internal detour inside another action, and
		// a "mine" status here would show the player a task they never asked for.
		s.rg.miner.gatherBlocks(ctx, blockName, scaffoldStockTarget)
	}
}

func (s *Scaffolder) DescendFromTower(ctx context.Context, targetY float32) {
	bot := s.rg.bot
	s.logger.Debug("Descending from tower", "target_y", targetY)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		curPos := bot.GetCoords()
		if curPos.Y() <= targetY+0.5 {
			break
		}

		refPos := protocol.BlockPos{
			int32(math.Floor(float64(curPos.X()))),
			int32(math.Floor(float64(curPos.Y()))) - 1,
			int32(math.Floor(float64(curPos.Z()))),
		}

		world := bot.GetLocalWorldModel()
		if !world.IsSolid(refPos.X(), refPos.Y(), refPos.Z()) {
			break
		}

		bot.LookAt(mgl32.Vec3{float32(refPos.X()) + 0.5, float32(refPos.Y()) + 0.5, float32(refPos.Z()) + 0.5})
		time.Sleep(100 * time.Millisecond)

		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   refPos,
			BlockFace:       1,
		})

		// Under server-auth block breaking the host honours PredictDestroy only
		// after the full vanilla break time elapsed; the fixed 400ms predates that
		// mode and leaves scaffold blocks standing.
		breakWait := 400 * time.Millisecond
		if serverAuthBreaking(bot) {
			breakWait = sabdBreakDuration(true, "dirt", "")
		}
		time.Sleep(breakWait)

		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionCrackBreak,
			BlockPosition:   refPos,
			BlockFace:       1,
		})
		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   refPos,
			BlockFace:       1,
		})
		// StopBreak must be the last packet of the sequence. Without it the server
		// still holds destroy-progress at this position, and the next block the
		// bot places here — a foundation, a wall, the start of a house — comes
		// back already broken. Every other break path in the bot ends the same
		// way (see miner.mineSingle).
		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStopBreak,
			BlockPosition:   refPos,
			BlockFace:       1,
		})

		world.SetSolid(refPos.X(), refPos.Y(), refPos.Z(), false)

		time.Sleep(200 * time.Millisecond)
	}
}
