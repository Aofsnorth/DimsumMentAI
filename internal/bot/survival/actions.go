package survival

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// ===================== BED SLEEPING =====================

// FindNearbyBed searches for a bed block within the given radius
func (m *Manager) FindNearbyBed(radius int32) (protocol.BlockPos, bool) {
	pos := m.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	var best protocol.BlockPos
	bestDist := math.MaxFloat64
	found := false

	for dx := -radius; dx <= radius; dx++ {
		for dy := int32(-2); dy <= 3; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				name, ok := m.bot.GetBlockName(bx+dx, by+dy, bz+dz)
				if !ok {
					continue
				}
				// Bed blocks contain "bed" in their name
				if containsAny(name, "bed") {
					d := float64(dx*dx + dy*dy + dz*dz)
					if d < bestDist {
						bestDist = d
						best = protocol.BlockPos{bx + dx, by + dy, bz + dz}
						found = true
					}
				}
			}
		}
	}
	return best, found
}

// SleepInBed attempts to use a nearby bed to sleep through the night
func (m *Manager) SleepInBed(ctx context.Context) bool {
	if !m.IsNight() {
		m.logger.Debug("SleepInBed: not nighttime, skipping")
		return false
	}

	bedPos, found := m.FindNearbyBed(12)
	if !found {
		m.bot.SendChat("Aku gak nemu kasur di sekitar sini.")
		return false
	}

	// Navigate to adjacent position
	approach := protocol.BlockPos{bedPos.X(), bedPos.Y(), bedPos.Z() + 1}
	if !m.bot.NavigateToBlock(approach.X(), approach.Y(), approach.Z(), 2.0) {
		m.logger.Warn("SleepInBed: could not reach bed", "pos", bedPos)
		return false
	}
	m.bot.StopMovement()

	// Look at the bed
	m.bot.LookAt(mgl32.Vec3{float32(bedPos.X()) + 0.5, float32(bedPos.Y()) + 0.5, float32(bedPos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	// Interact with bed (right-click to sleep)
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   bedPos,
			BlockFace:       1,
			HotBarSlot:      int32(m.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{},
			Position:        m.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 0.5, 0.5},
		},
	}
	if err := m.bot.WritePacket(tx); err != nil {
		m.logger.Warn("SleepInBed: failed to interact with bed", "err", err)
		return false
	}

	m.logger.Info("Sleeping in bed", "pos", bedPos)
	m.bot.SendChat("Aku tidur dulu ya, selamat malam! 🌙")
	return true
}

// ===================== TORCH PLACEMENT =====================

// PlaceTorch places a torch at the specified position
func (m *Manager) PlaceTorch(ctx context.Context, pos protocol.BlockPos) bool {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	// Find torch in inventory
	var torchSlot uint32
	found := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := names[item.NetworkID]
		if containsAny(name, "torch") && !containsAny(name, "redstone") && !containsAny(name, "soul_torch") {
			torchSlot = slot
			found = true
			break
		}
	}
	if !found {
		return false
	}

	// Equip torch
	if err := m.bot.EquipItem(torchSlot); err != nil {
		return false
	}
	time.Sleep(100 * time.Millisecond)

	// Look at the support block
	supportPos := protocol.BlockPos{pos.X(), pos.Y() - 1, pos.Z()}
	m.bot.LookAt(mgl32.Vec3{float32(supportPos.X()) + 0.5, float32(supportPos.Y()) + 1.0, float32(supportPos.Z()) + 0.5})
	time.Sleep(100 * time.Millisecond)

	// Place torch on top of support block
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   supportPos,
			BlockFace:       1, // top face
			HotBarSlot:      int32(torchSlot),
			HeldItem:        protocol.ItemInstance{Stack: inv[torchSlot]},
			Position:        m.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	if err := m.bot.WritePacket(tx); err != nil {
		return false
	}

	m.logger.Debug("Placed torch", "pos", pos)
	return true
}

// AutoPlaceTorches places torches around the bot when in dark areas
func (m *Manager) AutoPlaceTorches(ctx context.Context) {
	if !m.autoTorchOn || !m.AutoTorchEnabled {
		return
	}
	if time.Since(m.lastTorchTime) < 8*time.Second {
		return
	}

	// Place torch at bot's feet position if on solid ground
	pos := m.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	// Check if there's a solid block below and empty space at torch level
	world := m.bot.GetLocalWorldModel()
	if !world.IsSolid(bx, by-1, bz) {
		return // no solid ground
	}

	// Check if the space is already a torch
	name, ok := m.bot.GetBlockName(bx, by, bz)
	if ok && containsAny(name, "torch") {
		return
	}

	// Try to place torch on the ground next to us (on a wall or floor)
	torchPos := protocol.BlockPos{bx, by + 1, bz} // on the wall at head level
	if m.PlaceTorch(ctx, torchPos) {
		m.mu.Lock()
		m.lastTorchTime = time.Now()
		m.mu.Unlock()
	}
}

// ===================== EMERGENCY SHELTER =====================

// BuildEmergencyShelter builds a simple 3x3x2 dirt/cobblestone shelter
func (m *Manager) BuildEmergencyShelter(ctx context.Context) bool {
	if m.isSheltering {
		return false
	}
	m.isSheltering = true
	defer func() { m.isSheltering = false }()

	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	// Find building material (dirt, cobblestone, etc.)
	var buildSlot uint32
	buildCount := 0
	found := false

	buildMaterials := []string{"cobblestone", "dirt", "oak_planks", "stone", "sand"}
	for _, mat := range buildMaterials {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := names[item.NetworkID]
			if containsAny(name, mat) {
				buildSlot = slot
				buildCount = int(item.Count)
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found || buildCount < 12 {
		m.bot.SendChat("Gak punya cukup bahan buat bikin shelter darurat.")
		return false
	}

	m.bot.SendChat("Aku bikin shelter darurat dulu ya! 🏠")

	pos := m.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	if err := m.bot.EquipItem(buildSlot); err != nil {
		return false
	}

	// Build walls: 3x3 ring at head height and above
	placements := []protocol.BlockPos{}
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue // skip center (where bot stands)
			}
			// Wall at feet+1 level
			placements = append(placements, protocol.BlockPos{bx + dx, by + 1, bz + dz})
			// Wall at feet+2 level
			placements = append(placements, protocol.BlockPos{bx + dx, by + 2, bz + dz})
		}
	}
	// Roof
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			placements = append(placements, protocol.BlockPos{bx + dx, by + 3, bz + dz})
		}
	}

	placed := 0
	for _, p := range placements {
		if placed >= buildCount-1 {
			break
		}

		supportPos := protocol.BlockPos{p.X(), p.Y() - 1, p.Z()}
		m.bot.LookAt(mgl32.Vec3{float32(p.X()) + 0.5, float32(p.Y()), float32(p.Z()) + 0.5})
		time.Sleep(80 * time.Millisecond)

		tx := &packet.InventoryTransaction{
			TransactionData: &protocol.UseItemTransactionData{
				ActionType:      protocol.UseItemActionClickBlock,
				BlockPosition:   supportPos,
				BlockFace:       1,
				HotBarSlot:      int32(buildSlot),
				HeldItem:        protocol.ItemInstance{Stack: inv[buildSlot]},
				Position:        m.bot.GetCoords(),
				ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
			},
		}
		_ = m.bot.WritePacket(tx)
		placed++
		time.Sleep(120 * time.Millisecond)

		select {
		case <-ctx.Done():
			return false
		default:
		}
	}

	m.bot.SendChat("Shelter darurat selesai! Aman dari mob malam ini.")
	return true
}

// ===================== DEATH RECOVERY =====================

// RecoverFromDeath navigates to the death position to recover items
func (m *Manager) RecoverFromDeath(ctx context.Context) bool {
	deathPos, has := m.GetDeathPos()
	if !has {
		m.bot.SendChat("Aku gak inget mati di mana.")
		return false
	}

	m.bot.SendChat("Aku pergi ambil barang-barang yang jatuh ya!")
	m.bot.NavigateTo(deathPos)

	// Wait for arrival (max 60 seconds)
	deadline := time.After(60 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			m.bot.SendChat("Gak bisa sampai ke lokasi mati, terlalu jauh kayaknya.")
			m.ClearDeath()
			return false
		case <-ticker.C:
			pos := m.bot.GetCoords()
			dist := pos.Sub(deathPos).Len()
			if dist < 3.0 {
				m.bot.StopMovement()
				m.ClearDeath()
				m.bot.SendChat("Aku sampai di lokasi mati, aku ambil barang-barangnya!")
				return true
			}
		}
	}
}

// ===================== HELPER =====================

func containsAny(s string, needles ...string) bool {
	s = strings.ToLower(s)
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
