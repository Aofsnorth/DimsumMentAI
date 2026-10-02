package bot

import (
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Timing constants for bot actions
const (
	// EquipItemDelay is the delay after equipping an item before performing an action
	EquipItemDelay = 150 * time.Millisecond

	// InventoryOpenDelay is the delay after opening inventory before transactions
	InventoryOpenDelay = 150 * time.Millisecond

	// YawSyncTimeout is the maximum time to wait for yaw synchronization
	YawSyncTimeout = 1200 * time.Millisecond

	// AngleStabilizationDelay is the delay after setting look angles before action
	AngleStabilizationDelay = 200 * time.Millisecond

	// BlockPlacementTimeout bounds how long placement waits for an authoritative
	// UpdateBlock packet from the server.
	BlockPlacementTimeout = 3 * time.Second

	// DefaultDropPitch is the upward pitch angle for dropping items (degrees)
	// Negative pitch = looking up, causes item to arc forward
	DefaultDropPitch = -28.0

	// ActionDelayShort is a short delay after minor actions
	ActionDelayShort = 450 * time.Millisecond

	// ActionDelayMedium is a medium delay after significant actions
	ActionDelayMedium = 500 * time.Millisecond

	// MaxDropStackSize is the maximum number of items to drop in one transaction
	MaxDropStackSize = 64

	// MaxStackSize is the maximum stack size for items
	MaxStackSize = 64

	// MaxHealth is the maximum player health
	MaxHealth = 20

	// MaxHunger is the maximum player hunger
	MaxHunger = 20
)

// Bedrock protocol constants
const (
	// BlockFaceTop is the top face of a block (Y+)
	BlockFaceTop = 1

	// BlockCenterOffset is the center offset for clicking on a block face
	BlockCenterOffset = 0.5

	// YawOffsetDegrees is added to yaw for forward direction calculation
	YawOffsetDegrees = 90.0

	// DegreesToRadians conversion factor
	DegreesToRadians = 3.14159265358979323846 / 180.0

	// FullCircleDegrees is a full rotation in degrees
	FullCircleDegrees = 360.0

	// FloatEpsilon is the minimum distance threshold for movement calculations
	FloatEpsilon = 0.001

	// MinPlayerInteractDistance is the minimum distance before approaching player
	MinPlayerInteractDistance = 2.0

	// ApproachOffsetZ is the Z-axis offset when approaching a player
	ApproachOffsetZ = 1.3

	// PlayerEyeHeight is the Y-offset for player eye level
	PlayerEyeHeight = 1.62
)

// Protocol slot constants
const (
	// CraftingGridBaseSlot is the first slot of the player's personal 2x2
	// crafting grid in the player UI container.
	CraftingGridBaseSlot = 28

	// CraftingGrid1x1Slot is the slot for 2x2 crafting (personal inventory)
	CraftingGrid1x1Slot = 29

	// CraftingTableGridBaseSlot is the first slot of a crafting table's 3x3 input
	// in the same player UI container (slots 32..40).
	CraftingTableGridBaseSlot = 32

	// CreatedOutputSlot is the crafting result slot
	CreatedOutputSlot = 50
)

// ObstacleAim is the point the bot looks at while breaking an obstacle in its
// way: the top face centre, which is what a wedged player is staring at.
func ObstacleAim(pos protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 1, float32(pos.Z()) + 0.5}
}

// BlockCollidesWithBot returns true if the block at blockPos intersects the bot's AABB.
func BlockCollidesWithBot(blockPos protocol.BlockPos, botPos mgl32.Vec3) bool {
	botMinX := botPos.X() - 0.3
	botMaxX := botPos.X() + 0.3
	botMinY := botPos.Y()
	botMaxY := botPos.Y() + 1.8
	botMinZ := botPos.Z() - 0.3
	botMaxZ := botPos.Z() + 0.3

	bMinX := float32(blockPos.X())
	bMaxX := float32(blockPos.X() + 1)
	bMinY := float32(blockPos.Y())
	bMaxY := float32(blockPos.Y() + 1)
	bMinZ := float32(blockPos.Z())
	bMaxZ := float32(blockPos.Z() + 1)

	overlapX := botMinX < bMaxX && botMaxX > bMinX
	overlapY := botMinY < bMaxY && botMaxY > bMinY
	overlapZ := botMinZ < bMaxZ && botMaxZ > bMinZ

	return overlapX && overlapY && overlapZ
}

// Display and capacity constants
const (
	// ListCraftableLimit is the default display limit for craftable items
	ListCraftableLimit = 10

	// DefaultEmoteCount is the default number of emotes to execute
	DefaultEmoteCount = 10

	// DefaultActionCount is the default count for repeatable actions
	DefaultActionCount = 20

	// InitialCraftableCapacity is the initial slice capacity for craftable items
	InitialCraftableCapacity = 50
)
