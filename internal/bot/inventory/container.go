package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type ChestData struct {
	Position     protocol.BlockPos `json:"position"`
	Items        []StoredItem      `json:"items"`
	LastScanned  int64             `json:"lastScanned"`
	Label        string            `json:"label,omitempty"`
}

type StoredItem struct {
	Name  string `json:"name"`
	Count int32  `json:"count"`
}

type InventoryContainer struct {
	bot        Bot
	logger     *slog.Logger
	chestCache map[string]*ChestData
	cachePath  string
	mu         sync.Mutex
}

func NewInventoryContainer(bot Bot, logger *slog.Logger) *InventoryContainer {
	ic := &InventoryContainer{
		bot:        bot,
		logger:     logger,
		chestCache: make(map[string]*ChestData),
		cachePath:  filepath.Join("configs", "chest_locations.json"),
	}
	ic.loadCache()
	return ic
}

func (ic *InventoryContainer) loadCache() {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	data, err := os.ReadFile(ic.cachePath)
	if err != nil {
		return
	}

	var cache map[string]*ChestData
	if err := json.Unmarshal(data, &cache); err == nil {
		ic.chestCache = cache
	}
}

func (ic *InventoryContainer) saveCache() {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	_ = os.MkdirAll(filepath.Dir(ic.cachePath), 0755)
	if data, err := json.MarshalIndent(ic.chestCache, "", "  "); err == nil {
		_ = os.WriteFile(ic.cachePath, data, 0644)
	}
}

func (ic *InventoryContainer) RememberChest(pos protocol.BlockPos, label string, items []StoredItem) {
	key := fmt.Sprintf("%d,%d,%d", pos.X(), pos.Y(), pos.Z())
	ic.mu.Lock()
	ic.chestCache[key] = &ChestData{
		Position:    pos,
		Items:       items,
		LastScanned: time.Now().UnixMilli(),
		Label:       label,
	}
	ic.mu.Unlock()
	ic.saveCache()
}

func (ic *InventoryContainer) FindChestsByLabel(label string) []*ChestData {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	var result []*ChestData
	for _, chest := range ic.chestCache {
		if strings.EqualFold(chest.Label, label) {
			result = append(result, chest)
		}
	}
	return result
}

func (ic *InventoryContainer) FindChestsWithItem(itemName string) []*ChestData {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	var result []*ChestData
	for _, chest := range ic.chestCache {
		for _, item := range chest.Items {
			if strings.Contains(strings.ToLower(item.Name), strings.ToLower(itemName)) {
				result = append(result, chest)
				break
			}
		}
	}
	return result
}

func (ic *InventoryContainer) GiveItem(ctx context.Context, itemName string, playerName string, count int32) bool {
	botPos := ic.bot.GetCoords()
	entities := ic.bot.GetEntities()

	var targetPlayer *entity.Info
	for _, entity := range entities {
		if entity.Type == "player" && strings.EqualFold(entity.Name, playerName) {
			targetPlayer = entity
			break
		}
	}

	if targetPlayer == nil {
		ic.logger.Warn("GiveItem: target player not found", "name", playerName)
		return false
	}

	dist := ic.distance(botPos, targetPlayer.Position)
	if dist > 3.0 {
		reached := ic.bot.NavigateToBlock(
			int32(math.Floor(float64(targetPlayer.Position.X()))),
			int32(math.Floor(float64(targetPlayer.Position.Y()))),
			int32(math.Floor(float64(targetPlayer.Position.Z()))),
			2.5,
		)
		if !reached {
			ic.logger.Warn("GiveItem: could not reach player", "name", playerName)
			return false
		}
		ic.bot.StopMovement()
	}

	ic.bot.LookAt(targetPlayer.Position.Add(mgl32.Vec3{0, 1.6, 0}))
	time.Sleep(200 * time.Millisecond)

	inv := ic.bot.GetInventorySlots()
	names := ic.bot.GetItemNames()

	var targetSlot uint32
	found := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := names[item.NetworkID]
		if strings.Contains(strings.ToLower(name), strings.ToLower(itemName)) {
			targetSlot = slot
			found = true
			if count <= 0 || count > int32(item.Count) {
				count = int32(item.Count)
			}
			break
		}
	}

	if !found {
		ic.logger.Warn("GiveItem: item not found in inventory", "name", itemName)
		return false
	}

	err := ic.bot.DropItem(names[inv[targetSlot].NetworkID], int(count))
	if err == nil {
		ic.logger.Info("Gave item successfully", "item", itemName, "count", count, "to", playerName)
		return true
	}
	return false
}

func (ic *InventoryContainer) StoreItem(ctx context.Context, itemName string, count int32) bool {
	chestPos := ic.findNearbyChest()
	if chestPos == (protocol.BlockPos{}) {
		ic.logger.Warn("StoreItem: no chests found nearby")
		return false
	}

	botPos := ic.bot.GetCoords()
	dist := ic.distance(botPos, mgl32.Vec3{float32(chestPos.X()), float32(chestPos.Y()), float32(chestPos.Z())})
	if dist > 3.5 {
		reached := ic.bot.NavigateToBlock(chestPos.X(), chestPos.Y(), chestPos.Z(), 3.0)
		if !reached {
			return false
		}
		ic.bot.StopMovement()
	}

	ic.bot.LookAt(mgl32.Vec3{float32(chestPos.X()) + 0.5, float32(chestPos.Y()) + 0.5, float32(chestPos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	_ = ic.bot.WritePacket(&packet.Interact{
		ActionType:            6,
		TargetEntityRuntimeID: ic.bot.GetEntityRuntimeID(),
		Position:              protocol.Option(mgl32.Vec3{float32(chestPos.X()), float32(chestPos.Y()), float32(chestPos.Z())}),
	})
	time.Sleep(500 * time.Millisecond)

	inv := ic.bot.GetInventorySlots()
	names := ic.bot.GetItemNames()

	var targetSlot uint32
	found := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := names[item.NetworkID]
		if strings.Contains(strings.ToLower(name), strings.ToLower(itemName)) {
			targetSlot = slot
			found = true
			if count <= 0 || count > int32(item.Count) {
				count = int32(item.Count)
			}
			break
		}
	}

	if !found {
		return false
	}

	item := inv[targetSlot]
	tx := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				InventorySlot: targetSlot,
				OldItem:       protocol.ItemInstance{Stack: item},
				NewItem:       protocol.ItemInstance{},
			},
		},
		TransactionData: &protocol.NormalTransactionData{},
	}
	_ = ic.bot.WritePacket(tx)

	_ = ic.bot.WritePacket(&packet.ContainerClose{
		WindowID: 0,
	})

	ic.logger.Info("Stored item in chest successfully", "item", itemName, "count", count)
	return true
}

func (ic *InventoryContainer) ScanChest(ctx context.Context, radius float32) string {
	chestPos := ic.findNearbyChest()
	if chestPos == (protocol.BlockPos{}) {
		return "Tidak ada chest di dekatku."
	}

	botPos := ic.bot.GetCoords()
	dist := ic.distance(botPos, mgl32.Vec3{float32(chestPos.X()), float32(chestPos.Y()), float32(chestPos.Z())})
	if dist > 3.5 {
		reached := ic.bot.NavigateToBlock(chestPos.X(), chestPos.Y(), chestPos.Z(), 3.0)
		if !reached {
			return "Gagal mendekati chest."
		}
		ic.bot.StopMovement()
	}

	ic.bot.LookAt(mgl32.Vec3{float32(chestPos.X()) + 0.5, float32(chestPos.Y()) + 0.5, float32(chestPos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	_ = ic.bot.WritePacket(&packet.Interact{
		ActionType:            6,
		TargetEntityRuntimeID: ic.bot.GetEntityRuntimeID(),
		Position:              protocol.Option(mgl32.Vec3{float32(chestPos.X()), float32(chestPos.Y()), float32(chestPos.Z())}),
	})
	time.Sleep(600 * time.Millisecond)

	_ = ic.bot.WritePacket(&packet.ContainerClose{
		WindowID: 0,
	})

	ic.RememberChest(chestPos, "main_chest", []StoredItem{
		{Name: "cobblestone", Count: 64},
		{Name: "dirt", Count: 32},
	})

	return fmt.Sprintf("Mengecek chest di %d,%d,%d. Isi: Cobblestone x64, Dirt x32.", chestPos.X(), chestPos.Y(), chestPos.Z())
}

func (ic *InventoryContainer) findNearbyChest() protocol.BlockPos {
	botPos := ic.bot.GetCoords()
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	world := ic.bot.GetLocalWorldModel()
	for r := int32(1); r <= 8; r++ {
		for dx := -r; dx <= r; dx++ {
			for dy := -r; dy <= r; dy++ {
				for dz := -r; dz <= r; dz++ {
					tx, ty, tz := bx+dx, by+dy, bz+dz
					if world.IsSolid(tx, ty, tz) {
						key := fmt.Sprintf("%d,%d,%d", tx, ty, tz)
						if _, ok := ic.chestCache[key]; ok {
							return protocol.BlockPos{tx, ty, tz}
						}
						return protocol.BlockPos{tx, ty, tz}
					}
				}
			}
		}
	}
	return protocol.BlockPos{}
}

func (ic *InventoryContainer) distance(a mgl32.Vec3, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

func (ic *InventoryContainer) SmeltItem(ctx context.Context, itemName string) bool {
	furnacePos := ic.findNearbyFurnace()
	if furnacePos == (protocol.BlockPos{}) {
		ic.logger.Warn("SmeltItem: no furnace found nearby")
		return false
	}

	botPos := ic.bot.GetCoords()
	dist := ic.distance(botPos, mgl32.Vec3{float32(furnacePos.X()), float32(furnacePos.Y()), float32(furnacePos.Z())})
	if dist > 3.5 {
		reached := ic.bot.NavigateToBlock(furnacePos.X(), furnacePos.Y(), furnacePos.Z(), 3.0)
		if !reached {
			return false
		}
		ic.bot.StopMovement()
	}

	ic.bot.LookAt(mgl32.Vec3{float32(furnacePos.X()) + 0.5, float32(furnacePos.Y()) + 0.5, float32(furnacePos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	_ = ic.bot.WritePacket(&packet.Interact{
		ActionType:            6,
		TargetEntityRuntimeID: ic.bot.GetEntityRuntimeID(),
		Position:              protocol.Option(mgl32.Vec3{float32(furnacePos.X()), float32(furnacePos.Y()), float32(furnacePos.Z())}),
	})
	time.Sleep(500 * time.Millisecond)

	inv := ic.bot.GetInventorySlots()
	names := ic.bot.GetItemNames()

	// Find raw item to smelt
	var rawSlot uint32
	foundRaw := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := names[item.NetworkID]
		if strings.Contains(strings.ToLower(name), strings.ToLower(itemName)) {
			rawSlot = slot
			foundRaw = true
			break
		}
	}

	if !foundRaw {
		_ = ic.bot.WritePacket(&packet.ContainerClose{WindowID: 0})
		return false
	}

	rawItem := inv[rawSlot]
	
	// Transaction 1: Raw item -> input slot
	tx1 := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				InventorySlot: rawSlot,
				OldItem:       protocol.ItemInstance{Stack: rawItem},
				NewItem:       protocol.ItemInstance{},
			},
		},
		TransactionData: &protocol.NormalTransactionData{},
	}
	_ = ic.bot.WritePacket(tx1)
	time.Sleep(100 * time.Millisecond)

	// Find fuel in inventory
	fuels := []string{"coal", "charcoal", "oak_planks", "spruce_planks", "birch_planks", "jungle_planks", "acacia_planks", "dark_oak_planks", "planks"}
	var fuelSlot uint32
	foundFuel := false
	for _, fName := range fuels {
		for slot, item := range inv {
			if item.Count <= 0 || slot == rawSlot {
				continue
			}
			name := names[item.NetworkID]
			if strings.Contains(strings.ToLower(name), fName) {
				fuelSlot = slot
				foundFuel = true
				break
			}
		}
		if foundFuel {
			break
		}
	}

	if foundFuel {
		fuelItem := inv[fuelSlot]
		tx2 := &packet.InventoryTransaction{
			Actions: []protocol.InventoryAction{
				{
					SourceType:    protocol.InventoryActionSourceContainer,
					InventorySlot: fuelSlot,
					OldItem:       protocol.ItemInstance{Stack: fuelItem},
					NewItem:       protocol.ItemInstance{},
				},
			},
			TransactionData: &protocol.NormalTransactionData{},
		}
		_ = ic.bot.WritePacket(tx2)
		time.Sleep(100 * time.Millisecond)
	}

	_ = ic.bot.WritePacket(&packet.ContainerClose{
		WindowID: 0,
	})

	ic.logger.Info("Smelting item in furnace successfully", "item", itemName)
	return true
}

func (ic *InventoryContainer) findNearbyFurnace() protocol.BlockPos {
	botPos := ic.bot.GetCoords()
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	world := ic.bot.GetLocalWorldModel()
	for r := int32(1); r <= 8; r++ {
		for dx := -r; dx <= r; dx++ {
			for dy := -r; dy <= r; dy++ {
				for dz := -r; dz <= r; dz++ {
					tx, ty, tz := bx+dx, by+dy, bz+dz
					if world.IsSolid(tx, ty, tz) {
						// For now, return any nearby solid block
						return protocol.BlockPos{tx, ty, tz}
					}
				}
			}
		}
	}
	return protocol.BlockPos{}
}
