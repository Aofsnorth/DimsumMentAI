package combat_test

import (
	"bedrock-ai/internal/bot/affordance"
	"context"
	"math"
	"testing"

	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// dragonFakeBot is the recording bot plus the two readings the dragon fight
// needs and the plain combat fake does not have: an inventory to hold a bow in,
// and a health number that can be dropped low.
type dragonFakeBot struct {
	*recordingFakeBot
	slots  map[uint32]protocol.ItemStack
	names  map[int32]string
	health int
	stops  int
}

func (f *dragonFakeBot) GetInventorySlots() map[uint32]protocol.ItemStack { return f.slots }
func (f *dragonFakeBot) GetItemNames() map[int32]string                   { return f.names }
func (f *dragonFakeBot) GetStatusDetails() (int, int, string) {
	return f.health, 20, ""
}
func (f *dragonFakeBot) StopMovement() { f.stops++ }

func newDragonFakeBot(origin mgl32.Vec3) *dragonFakeBot {
	f := &dragonFakeBot{
		recordingFakeBot: newRecordingFakeBot(origin),
		slots: map[uint32]protocol.ItemStack{
			0: {ItemType: protocol.ItemType{NetworkID: 1}, Count: 1},  // bow
			1: {ItemType: protocol.ItemType{NetworkID: 2}, Count: 16}, // arrows
		},
		names:  map[int32]string{1: "minecraft:bow", 2: "minecraft:arrow"},
		health: 20,
	}
	// The dragon fights above the height the mob tests load, so the world the
	// line-of-sight ray walks has to reach that high.
	f.blocks = fakeBlockReader{loaded: loadedColumn(-40, 40, 0, 120)}
	return f
}

// loadedColumn is stripLoaded for a fight that happens in the air.
func loadedColumn(minX, maxX, minY, maxY int32) func(x, y, z int32) bool {
	return func(x, y, z int32) bool {
		return x >= minX && x <= maxX && y >= minY && y <= maxY && z == 0
	}
}

func addDragon(f *combatFakeBot, id uint64, pos mgl32.Vec3) {
	f.actors[id] = &entity.Info{
		ID: id, Type: "minecraft:ender_dragon", Name: "minecraft:ender_dragon",
		Position: pos, Health: 200,
	}
}

func addCrystal(f *combatFakeBot, id uint64, pos mgl32.Vec3) {
	f.actors[id] = &entity.Info{
		ID: id, Type: "minecraft:end_crystal", Name: "minecraft:end_crystal",
		Position: pos, Health: 1,
	}
}

// swingTargets returns the entities a melee packet was aimed at, so a test can
// ask who got hit rather than how many packets were written.
func swingTargets(t *testing.T, f *dragonFakeBot) []uint64 {
	t.Helper()
	f.combatFakeBot.mu.Lock()
	defer f.combatFakeBot.mu.Unlock()
	var out []uint64
	for _, pk := range f.packets {
		tx, ok := pk.(*packet.InventoryTransaction)
		if !ok {
			continue
		}
		if data, isHit := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData); isHit {
			out = append(out, data.TargetEntityRuntimeID)
		}
	}
	return out
}

func drewBow(f *dragonFakeBot) bool {
	f.combatFakeBot.mu.Lock()
	defer f.combatFakeBot.mu.Unlock()
	for _, pk := range f.packets {
		tx, ok := pk.(*packet.InventoryTransaction)
		if !ok {
			continue
		}
		if _, isDraw := tx.TransactionData.(*protocol.UseItemTransactionData); isDraw {
			return true
		}
	}
	return false
}

func TestTick_DragonWithCrystalInRange_ShootsTheCrystalNotTheDragon(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	dragon := mgl32.Vec3{0, 67, 0}
	crystal := mgl32.Vec3{12, 64, 0}
	addDragon(f.combatFakeBot, 5, dragon)
	addCrystal(f.combatFakeBot, 9, crystal)

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	if !drewBow(f) {
		t.Fatal("no draw transaction: the bot should be shooting the crystal, not the dragon")
	}
	if hits := swingTargets(t, f); len(hits) != 0 {
		t.Fatalf("swing packets aimed at %v, want none: the dragon is not in reach", hits)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.looks) == 0 {
		t.Fatal("no LookAt issued during the crystal shot")
	}
	aim := f.looks[0]
	if d := combat.HorizontalDistance(aim, crystal); d > combat.CrystalBandMax {
		t.Fatalf("aim %v is %v blocks from the crystal, want the crystal targeted", aim, d)
	}
	if combat.HorizontalDistance(aim, dragon) < combat.HorizontalDistance(aim, crystal) {
		t.Fatalf("aim %v is nearer the dragon than the crystal", aim)
	}
}

func TestTick_DragonFlyingNoCrystals_HoldsGroundAndNeverCharges(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	addDragon(f.combatFakeBot, 5, mgl32.Vec3{30, 90, 0}) // out of reach, far overhead

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	if hits := swingTargets(t, f); len(hits) != 0 {
		t.Fatalf("swing packets aimed at %v, want none against a dragon thirty blocks up", hits)
	}
	if drewBow(f) {
		t.Fatal("the bow was drawn at a dragon it cannot reach")
	}
	// Nothing to shoot and nothing to walk to, so the bot stands where it is.
	// Chasing the dragon's shadow around a small island is how a bot walks off
	// the obsidian into the void.
	if f.stops == 0 {
		t.Fatal("StopMovement was never called, want the bot to hold its ground")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 0 {
		t.Fatalf("NavigateTo calls = %d, want none: %v is a walk across the island for nothing", len(f.navs), f.navs[0])
	}
}

func TestTick_CrystalInsideTheShootingBand_HoldsInsteadOfStrafing(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	addDragon(f.combatFakeBot, 5, mgl32.Vec3{0, 67, 0})
	addCrystal(f.combatFakeBot, 9, mgl32.Vec3{12, 64, 0}) // inside the 6-16 band

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	if !drewBow(f) {
		t.Fatal("the crystal was not shot")
	}
	// The End island is small and everything past the edge is a fall into the
	// void, so a comfortable shooting position is one the bot stands still in.
	if f.stops == 0 {
		t.Fatal("StopMovement was never called, want the bot to hold its firing position")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 0 {
		t.Fatalf("NavigateTo calls = %d, want none: strafing here walks the bot off the obsidian", len(f.navs))
	}
}

func TestTick_CrystalOutOfShootingRange_WalksTowardIt(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	addDragon(f.combatFakeBot, 5, mgl32.Vec3{30, 90, 0})
	addCrystal(f.combatFakeBot, 9, mgl32.Vec3{45, 64, 0}) // past the 32-block horizon

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	if drewBow(f) {
		t.Fatal("the bow was drawn at a crystal forty-five blocks away")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want one order closing on the crystal", len(f.navs))
	}
	// Ground level, on the way to the pillar: the dragon's altitude is not
	// somewhere the bot can walk to.
	if got := f.navs[0]; got != (mgl32.Vec3{45, 64, 0}) {
		t.Fatalf("walked to %v, want the crystal's ground position (45,64,0)", got)
	}
}

func TestTick_StandingDirectlyUnderTheDragon_MovesOutFromUnderIt(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	addDragon(f.combatFakeBot, 5, mgl32.Vec3{0, 80, 0}) // directly overhead

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want the bot to step out from under the dragon", len(f.navs))
	}
	dragon := mgl32.Vec3{0, 80, 0}
	if d := combat.HorizontalDistance(f.navs[0], dragon); d <= combat.BeamCorridorRadius {
		t.Fatalf("stepped to %v, only %v blocks off the dragon's column", f.navs[0], d)
	}
}

func TestTick_DragonPerchedNoCrystals_MeleesTheDragon(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	addDragon(f.combatFakeBot, 5, mgl32.Vec3{2, 66, 0}) // perched and in reach

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	hits := swingTargets(t, f)
	if len(hits) != 1 || hits[0] != 5 {
		t.Fatalf("swing targets = %v, want exactly the dragon 5", hits)
	}
}

func TestTick_DragonFightLowHealth_RetreatsWithoutSwinging(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	f.health = 5
	addDragon(f.combatFakeBot, 5, mgl32.Vec3{2, 66, 0}) // right in reach, and still a trap

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	if hits := swingTargets(t, f); len(hits) != 0 {
		t.Fatalf("swung at %v while at five health, want a retreat instead", hits)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want one retreat order", len(f.navs))
	}
	dragon := mgl32.Vec3{2, 66, 0}
	if d := combat.HorizontalDistance(f.navs[0], dragon); math.Abs(float64(d-combat.DragonRetreatDistance)) > 1e-3 {
		t.Fatalf("retreat stopped %v blocks from the dragon, want %v", d, combat.DragonRetreatDistance)
	}
	if f.navs[0].Z() == 0 {
		t.Fatalf("retreat to %v is straight backwards down the same line", f.navs[0])
	}
}

func TestTick_ClosingOnACrystalUnderTheDragon_StepsOutOfTheBeam(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	crystal := mgl32.Vec3{20, 64, 0}
	dragon := mgl32.Vec3{21, 80, 0} // circling directly over the crystal
	addDragon(f.combatFakeBot, 5, dragon)
	addCrystal(f.combatFakeBot, 9, crystal)

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want one order", len(f.navs))
	}
	if combat.InHealingBeam(f.navs[0], dragon, 1) {
		t.Fatalf("walked to %v, which is straight under the dragon", f.navs[0])
	}
	if f.navs[0] == crystal {
		t.Fatal("walked to the crystal regardless of the dragon above it")
	}
}

func TestTick_CrystalBehindObsidian_ClosesInsteadOfShooting(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	f.solidity = fakeSolidity{solid: func(x, y, z int32) bool { return x == 6 && y == 65 }}
	addDragon(f.combatFakeBot, 5, mgl32.Vec3{0, 67, 0})
	addCrystal(f.combatFakeBot, 9, mgl32.Vec3{20, 64, 0}) // beyond the shooting band

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	if drewBow(f) {
		t.Fatal("the bow was drawn at a crystal behind obsidian")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want one closing order", len(f.navs))
	}
	if d := combat.HorizontalDistance(f.navs[0], mgl32.Vec3{20, 64, 0}); d > combat.CrystalBandMax {
		t.Fatalf("closed to %v, want a point within the shooting band of the crystal", f.navs[0])
	}
}

func TestTick_CrystalInReachWithNoBow_IsHitWithTheSword(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	delete(f.slots, 0) // no bow
	delete(f.slots, 1) // and nothing to shoot with it
	f.names[3] = "minecraft:iron_sword"
	f.slots[3] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 3}, Count: 1}
	addDragon(f.combatFakeBot, 5, mgl32.Vec3{0, 67, 0})
	addCrystal(f.combatFakeBot, 9, mgl32.Vec3{2, 64, 0})

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	hits := swingTargets(t, f)
	if len(hits) != 1 || hits[0] != 9 {
		t.Fatalf("swing targets = %v, want the crystal 9 in reach rather than the dragon", hits)
	}
}

func TestTick_OrdinaryMob_IsUnaffectedByTheDragonCode(t *testing.T) {
	t.Parallel()

	f := newDragonFakeBot(mgl32.Vec3{0, 64, 0})
	addMob(f.combatFakeBot, 5, "skeleton", 9) // inside the strafe band
	addDragon(f.combatFakeBot, 6, mgl32.Vec3{30, 90, 0})
	addCrystal(f.combatFakeBot, 7, mgl32.Vec3{25, 64, 0})

	cm := combat.NewCombatManager(f, f.logger)
	cm.EngageTarget(5)
	cm.Tick(context.Background())

	// The dragon branch keys on what the bot is fighting, not on what else is
	// standing in the arena: a skeleton in the band still gets the skeleton
	// tactic, and the crystal in the corner is nobody's target.
	if !cm.InCombat() {
		t.Fatal("InCombat() = false, want the skeleton engagement untouched")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.navs) != 1 {
		t.Fatalf("NavigateTo calls = %d, want one strafe order", len(f.navs))
	}
	skeleton := mgl32.Vec3{9, 64, 0}
	if d := combat.HorizontalDistance(f.navs[0], skeleton); math.Abs(float64(d-9)) > 1e-3 {
		t.Fatalf("strafed to %v, want the skeleton's 9-block band held", f.navs[0])
	}
}

// The disposition is read per tick by the tactic, so the fake has to answer it.
// Careful is the right default here: it is the tier a fresh bot runs at, and a
// fake that answered Reckless would test the wrong branch by accident.
func (f *dragonFakeBot) Appetite() affordance.Appetite { return affordance.Careful }

// The composed defence needs a block and a placement that confirms. The fake
// answers "nothing to place", which is the honest zero: the tactic must still
// flee, and the wall is the better answer only when there is a wall to put up.
func (f *dragonFakeBot) FindScaffoldItem() (uint32, protocol.ItemStack, bool) {
	return 0, protocol.ItemStack{}, false
}

func (f *dragonFakeBot) PlaceShield(ctx context.Context, threat mgl32.Vec3) bool { return false }
