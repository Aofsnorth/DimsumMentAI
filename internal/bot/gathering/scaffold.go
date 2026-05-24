package gathering

import (
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

func (s *Scaffolder) TowerUpTo(ctx context.Context, targetY float32) {
	bot := s.rg.bot
	s.logger.Info("Towering up", "target_y", targetY)

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
				HotBarSlot:      int32(bot.GetHeldItemSlot()),
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

func (s *Scaffolder) DescendFromTower(ctx context.Context, targetY float32) {
	bot := s.rg.bot
	s.logger.Info("Descending from tower", "target_y", targetY)

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

		time.Sleep(400 * time.Millisecond)

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

		world.SetSolid(refPos.X(), refPos.Y(), refPos.Z(), false)

		time.Sleep(200 * time.Millisecond)
	}
}
