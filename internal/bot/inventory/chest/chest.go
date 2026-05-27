package chest

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
	FindPlayer(username string) (uint64, mgl32.Vec3, bool)
}

type ChestData struct {
	Position    protocol.BlockPos `json:"position"`
	Items       []StoredItem      `json:"items"`
	LastScanned int64             `json:"lastScanned"`
	Label       string            `json:"label,omitempty"`
}

type StoredItem struct {
	Name  string `json:"name"`
	Count int32  `json:"count"`
}

type Container struct {
	bot        Bot
	logger     *slog.Logger
	chestCache map[string]*ChestData
	cachePath  string
	mu         sync.Mutex
}

func NewContainer(bot Bot, logger *slog.Logger) *Container {
	c := &Container{
		bot:        bot,
		logger:     logger,
		chestCache: make(map[string]*ChestData),
		cachePath:  filepath.Join("configs", "chest_locations.json"),
	}
	c.loadCache()
	return c
}

func (c *Container) loadCache() {
	c.mu.Lock()
	go func() {
		// Just to release lock context properly if needed, but simple lock is fine.
	}()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.cachePath)
	if err != nil {
		return
	}

	var cache map[string]*ChestData
	if err := json.Unmarshal(data, &cache); err == nil {
		c.chestCache = cache
	}
}

func (c *Container) saveCache() {
	c.mu.Lock()
	defer c.mu.Unlock()

	_ = os.MkdirAll(filepath.Dir(c.cachePath), 0755)
	if data, err := json.MarshalIndent(c.chestCache, "", "  "); err == nil {
		_ = os.WriteFile(c.cachePath, data, 0644)
	}
}

func (c *Container) RememberChest(pos protocol.BlockPos, label string, items []StoredItem) {
	key := fmt.Sprintf("%d,%d,%d", pos.X(), pos.Y(), pos.Z())
	c.mu.Lock()
	c.chestCache[key] = &ChestData{
		Position:    pos,
		Items:       items,
		LastScanned: time.Now().UnixMilli(),
		Label:       label,
	}
	c.mu.Unlock()
	c.saveCache()
}

func (c *Container) FindChestsByLabel(label string) []*ChestData {
	c.mu.Lock()
	defer c.mu.Unlock()

	var result []*ChestData
	for _, chest := range c.chestCache {
		if strings.EqualFold(chest.Label, label) {
			result = append(result, chest)
		}
	}
	return result
}

func (c *Container) FindChestsWithItem(itemName string) []*ChestData {
	c.mu.Lock()
	defer c.mu.Unlock()

	var result []*ChestData
	for _, chest := range c.chestCache {
		for _, item := range chest.Items {
			if strings.Contains(strings.ToLower(item.Name), strings.ToLower(itemName)) {
				result = append(result, chest)
				break
			}
		}
	}
	return result
}

func (c *Container) HasChestAt(pos protocol.BlockPos) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fmt.Sprintf("%d,%d,%d", pos.X(), pos.Y(), pos.Z())
	_, ok := c.chestCache[key]
	return ok
}
