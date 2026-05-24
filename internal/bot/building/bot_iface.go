package building

import (
	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// BotInterface defines the methods required by the building subsystem from the main Bot struct.
type BotInterface interface {
	GetCoords() mgl32.Vec3
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	GetEntities() map[uint64]*entity.Info
	GetLocalWorldModel() entity.WorldModel
	SendSafeChat(msg string)
	WritePacket(pk packet.Packet) error
	GetHeldItemSlot() uint32
	EquipItem(slot uint32) error
	LookAt(pos mgl32.Vec3)
	GetPlayerCoords(username string) (mgl32.Vec3, bool)
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	CraftItem(recipeNetID uint32, count int) error
	GetRecipes() map[string]uint32
	GetEntityRuntimeID() uint64
	DropItem(name string, count int) error
	StopMovement()
}

