package gathering

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type Bot interface {
	GetCoords() mgl32.Vec3
	WritePacket(pk packet.Packet) error
	GetEntities() map[uint64]*entity.Info
	NavigateTo(pos mgl32.Vec3)
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	InjectAIEvent(msg string)
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	EquipItem(slot uint32) error
	UnequipItem() error
	SendChat(msg string)
	GetEntityRuntimeID() uint64
	GetLocalWorldModel() entity.WorldModel
	DropItem(name string, count int) error
	GetBlockName(x, y, z int32) (string, bool)
}

type ResourceGatherer struct {
	bot              Bot
	logger           *slog.Logger
	isGathering      bool
	choppedPositions map[string]bool
	mu               sync.Mutex

	// Sub-components
	chopper  *TreeChopper
	miner    *BlockMiner
	looter   *Looter
	scaffold *Scaffolder
}

func NewResourceGatherer(bot Bot, logger *slog.Logger) *ResourceGatherer {
	rg := &ResourceGatherer{
		bot:              bot,
		logger:           logger,
		choppedPositions: make(map[string]bool),
	}
	rg.chopper = NewTreeChopper(rg, logger)
	rg.miner = NewBlockMiner(rg, logger)
	rg.looter = NewLooter(rg, logger)
	rg.scaffold = NewScaffolder(rg, logger)
	return rg
}

func (rg *ResourceGatherer) IsGathering() bool {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	return rg.isGathering
}

func (rg *ResourceGatherer) SetGathering(gathering bool) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	rg.isGathering = gathering
}

func (rg *ResourceGatherer) EnsureInventorySpace(ctx context.Context) bool {
	inv := rg.bot.GetInventorySlots()
	freeSlots := 36 - len(inv) // standard inventory size
	if freeSlots >= 2 {
		return true
	}

	rg.logger.Info("Inventory full, trying to drop junk items")
	// Try to drop common junk items: seeds, cobblestone (if excessive), dirt (if excessive)
	junkTypes := []string{"seed", "seeds", "wheat_seeds", "beetroot_seeds", "melon_seeds", "pumpkin_seeds"}

	names := rg.bot.GetItemNames()
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := names[item.NetworkID]
		for _, junk := range junkTypes {
			if strings.Contains(strings.ToLower(name), strings.ToLower(junk)) {
				_ = rg.bot.DropItem(name, int(item.Count))
				time.Sleep(200 * time.Millisecond)
				rg.logger.Info("Dropped junk item to make space", "name", name, "slot", slot)
				return true
			}
		}
	}

	return false
}

func (rg *ResourceGatherer) GatherWood(ctx context.Context, targetCount int) {
	rg.GatherWoodType(ctx, "", targetCount)
}

func (rg *ResourceGatherer) GatherWoodType(ctx context.Context, preferred string, targetCount int) {
	rg.SetGathering(true)
	defer rg.SetGathering(false)

	if !rg.EnsureInventorySpace(ctx) {
		rg.bot.SendChat("Inventory aku penuh banget, tolong kosongin dulu dong!")
		return
	}

	rg.chopper.GatherWood(ctx, targetCount, preferred)
}

func (rg *ResourceGatherer) GatherBlock(ctx context.Context, blockName string, targetCount int) {
	rg.SetGathering(true)
	defer rg.SetGathering(false)

	if !rg.EnsureInventorySpace(ctx) {
		rg.bot.SendChat("Inventory aku penuh banget, tolong kosongin dulu!")
		return
	}

	rg.miner.GatherBlock(ctx, blockName, targetCount)
}

func (rg *ResourceGatherer) CollectAllDrops(ctx context.Context, maxDist float32) int {
	rg.SetGathering(true)
	defer rg.SetGathering(false)

	return rg.looter.CollectAllDrops(ctx, maxDist)
}

func (rg *ResourceGatherer) Stop() {
	rg.SetGathering(false)
	rg.bot.StopMovement()
}

func (rg *ResourceGatherer) TowerUp(ctx context.Context, height int) {
	if height <= 0 {
		return
	}
	pos := rg.bot.GetCoords()
	rg.scaffold.TowerUpTo(ctx, pos.Y()+float32(height))
}

func (rg *ResourceGatherer) DigDown(ctx context.Context, depth int) {
	if depth <= 0 {
		return
	}
	pos := rg.bot.GetCoords()
	targetY := pos.Y() - float32(depth)
	rg.scaffold.DescendFromTower(ctx, targetY)
}

func (rg *ResourceGatherer) FindScaffoldItem() (uint32, protocol.ItemStack, bool) {
	return rg.scaffold.FindScaffoldItem()
}

func (rg *ResourceGatherer) TowerUpTo(ctx context.Context, targetY float32) {
	rg.scaffold.TowerUpTo(ctx, targetY)
}
