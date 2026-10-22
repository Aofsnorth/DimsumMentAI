// The duel contract, from the player's seat: the bot fights back when hit,
// keeps its swings inside survival reach, guards with a shield the server
// actually counts, and reaches for the axe only when the target has proven
// armoured. Every fair-play rule the doctrine promises is a rule this file
// can watch being kept.
package combat_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"bedrock-ai/internal/bot/combat"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

var errFakeEquip = errors.New("fake off-hand refusal")

// TestPvPReachIsFair: a survival Bedrock client reaches 3 blocks. A swing at
// 3.4 is the thing anticheats and opponents both read as a reach hack, so the
// tick must refuse it and then take it once the body has closed the gap.
func TestPvPReachIsFair(t *testing.T) {
	t.Parallel()
	f := newCombatFakeBot(mgl32.Vec3{0, 64, 0}, stripLoaded(-2, 8), noSolid)
	f.players["Steve"] = struct {
		id  uint64
		pos mgl32.Vec3
	}{id: 42, pos: mgl32.Vec3{3.4, 64, 0}}
	cm := combat.NewCombatManager(f, f.logger)

	if !cm.EngagePlayer("steve") {
		t.Fatal("EngagePlayer(steve) = false, want tracked player")
	}
	cm.Tick(context.TODO())

	f.mu.Lock()
	outOfReach := len(f.packets)
	f.mu.Unlock()
	if outOfReach != 0 {
		t.Fatalf("swung at %.1f blocks: %d packets, want 0 — past 3 blocks is reach, not PvP", 3.4, outOfReach)
	}

	// The same duel, target now inside reach: the swing goes out.
	f.mu.Lock()
	f.players["Steve"] = struct {
		id  uint64
		pos mgl32.Vec3
	}{id: 42, pos: mgl32.Vec3{2.0, 64, 0}}
	f.mu.Unlock()
	cm.Tick(context.TODO())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.packets) < 2 {
		t.Fatalf("in-reach swings = %d packets, want animate + transaction", len(f.packets))
	}
	tx, ok := f.packets[len(f.packets)-1].(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("last packet = %T, want the attack transaction", f.packets[len(f.packets)-1])
	}
	data, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData)
	if !ok {
		t.Fatalf("transaction data = %T, want UseItemOnEntityTransactionData", tx.TransactionData)
	}
	if data.TargetEntityRuntimeID != 42 {
		t.Fatalf("attack target = %d, want player entity 42", data.TargetEntityRuntimeID)
	}
}

// retaliatingBot wraps the combat fake with the tracked-player read the
// self-defence reflex needs. Players are not actors, so the reflex cannot
// find them through GetEntities — it needs the read that looks where players
// actually live.
type retaliatingBot struct {
	*combatFakeBot
	mu      sync.Mutex
	tracked map[string]mgl32.Vec3
	ids     map[string]uint64
}

// newRetaliatingBot stages players both as tracked positions (the reflex read)
// and as entities FindPlayer can resolve (what EngageDefender uses).
func newRetaliatingBot(tracked map[string]mgl32.Vec3) (*retaliatingBot, *combat.CombatManager, *combat.ThreatDetector) {
	base := newCombatFakeBot(mgl32.Vec3{0, 64, 0}, stripLoaded(-32, 32), noSolid)
	r := &retaliatingBot{
		combatFakeBot: base,
		tracked:       tracked,
		ids:           map[string]uint64{},
	}
	n := uint64(7)
	for name, pos := range tracked {
		r.ids[name] = n
		base.players[name] = struct {
			id  uint64
			pos mgl32.Vec3
		}{id: n, pos: pos}
		n++
	}
	cm := combat.NewCombatManager(base, base.logger)
	td := combat.NewThreatDetector(r, cm, base.logger)
	return r, cm, td
}

func (r *retaliatingBot) NearestTrackedPlayer(origin mgl32.Vec3, maxDist float32) (string, uint64, mgl32.Vec3, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	best := maxDist
	name, id, pos, found := "", uint64(0), mgl32.Vec3{}, false
	for pname, ppos := range r.tracked {
		d := origin.Sub(ppos).Len()
		if d <= best {
			best, name, id, pos, found = d, pname, r.ids[pname], ppos, true
		}
	}
	return name, id, pos, found
}

// lastEvent returns the most recent AI event, which is how these tests read
// what the bot was told to say.
func (f *combatFakeBot) lastEvent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.aiEvents) == 0 {
		return ""
	}
	return f.aiEvents[len(f.aiEvents)-1]
}

// hitOnce models what the packet layer does for one real player hit: the
// attacker's arm swing arrives first, then the health drop.
func hitOnce(td *combat.ThreatDetector, attackerID uint64, feet mgl32.Vec3) {
	td.NotePlayerSwing(attackerID)
	td.OnDamaged(feet)
}

// TestFirstHitWarnsInsteadOfFighting: one bare-handed punch is not a duel.
// The bot turns, says something, and does not swing. The old reflex answered a
// single poke with a sword and a 32-block chase, which is the "agresif banget"
// report this ladder exists for.
func TestFirstHitWarnsInsteadOfFighting(t *testing.T) {
	t.Parallel()
	r, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Attacker": {2.0, 64, 0}})

	hitOnce(td, r.ids["Attacker"], mgl32.Vec3{0, 64, 0})

	if cm.InCombat() {
		t.Fatal("a single hit started a fight — the bot must warn first")
	}
	if got := r.attackTransactions(); got != 0 {
		t.Fatalf("a single hit produced %d swings, want 0", got)
	}
	if got := r.lastEvent(); got == "" {
		t.Fatal("a single hit produced no reaction at all; the bot must say something")
	}
}

// TestRepeatedHitsAnswerDefensively: the second and third hit inside one
// scuffle earn a defensive answer — not the full duel doctrine.
func TestRepeatedHitsAnswerDefensively(t *testing.T) {
	t.Parallel()
	r, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Attacker": {2.0, 64, 0}})
	id := r.ids["Attacker"]

	hitOnce(td, id, mgl32.Vec3{0, 64, 0})
	hitOnce(td, id, mgl32.Vec3{0, 64, 0})

	if !cm.InCombat() {
		t.Fatal("the second hit produced no answer at all")
	}
	if got := cm.ModeForTest(); got != combat.ModeDefend {
		t.Fatalf("mode after two hits = %v, want defend — a short scuffle is not a duel", got)
	}
	if got := cm.PvpTargetForTest(); got != "Attacker" {
		t.Fatalf("defended against %q, want Attacker", got)
	}
}

// TestSustainedAttackBecomesADuel: somebody who keeps swinging after three
// answers is actually fighting, and earns the duel.
func TestSustainedAttackBecomesADuel(t *testing.T) {
	t.Parallel()
	r, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Attacker": {2.0, 64, 0}})
	id := r.ids["Attacker"]

	for i := 0; i < 4; i++ {
		hitOnce(td, id, mgl32.Vec3{0, 64, 0})
	}

	if got := cm.ModeForTest(); got != combat.ModeDuel {
		t.Fatalf("mode after four hits = %v, want duel", got)
	}
}

// TestDamageWithNoPlayerSwingIsNotRetaliation: fall damage, mobs and poison
// arrive as the same health drop. A player standing nearby is not evidence.
func TestDamageWithNoPlayerSwingIsNotRetaliation(t *testing.T) {
	t.Parallel()
	_, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Bystander": {2.0, 64, 0}})

	// No swing, no SetLastHurtBy: this is a fall next to a bystander.
	td.OnDamaged(mgl32.Vec3{0, 64, 0})

	if cm.InCombat() {
		t.Fatal("retaliated over damage nobody caused — closeness was read as guilt")
	}
}

// TestSetLastHurtByAloneAttributesToThePlayer: some servers do not relay the
// attacker's Animate packet, so the server's own last-hurt-by verdict has to
// stand on its own.
func TestSetLastHurtByAloneAttributesToThePlayer(t *testing.T) {
	t.Parallel()
	_, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Attacker": {2.0, 64, 0}})

	td.NotePlayerDamage()
	td.OnDamaged(mgl32.Vec3{0, 64, 0})
	td.OnDamaged(mgl32.Vec3{0, 64, 0})

	if !cm.InCombat() {
		t.Fatal("SetLastHurtBy said a player did it and the bot still did not answer")
	}
}

// TestAllyIsNeverFought: the player the bot answers to can poke it as much as
// they like. Hitting them back is a bug, not a reflex.
func TestAllyIsNeverFought(t *testing.T) {
	t.Parallel()
	r, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Owner": {2.0, 64, 0}})
	td.SetAllyFilter(func(name string) bool { return name == "Owner" })

	for i := 0; i < 8; i++ {
		hitOnce(td, r.ids["Owner"], mgl32.Vec3{0, 64, 0})
	}

	if cm.InCombat() {
		t.Fatal("the bot fought the player it answers to")
	}
	if r.lastEvent() == "" {
		t.Fatal("an owner hit must still get a reaction, just not a swing")
	}
}

// TestOnDamagedWithoutPlayersNearbyStaysCalm: nothing in reach means nothing
// to answer, no matter what hurt the bot.
func TestOnDamagedWithoutPlayersNearbyStaysCalm(t *testing.T) {
	t.Parallel()
	_, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"FarAway": {40, 64, 0}})

	td.OnDamaged(mgl32.Vec3{0, 64, 0})

	if cm.InCombat() {
		t.Fatal("engaged a player 40 blocks away over damage nobody nearby caused")
	}
}

// shieldBot wraps the combat fake with the off-hand and crouch controls the
// blocking state owns.
type shieldBot struct {
	*combatFakeBot
	offhandSlot uint32
	offhandErr  error
	sneak       bool
}

func (s *shieldBot) EquipToOffhand(slot uint32) error {
	s.offhandSlot = slot
	return s.offhandErr
}

func (s *shieldBot) SetSneakHeld(held bool) { s.sneak = held }

func newShieldTestBot() *shieldBot {
	base := newCombatFakeBot(mgl32.Vec3{0, 64, 0}, stripLoaded(-2, 8), noSolid)
	base.inventory = map[uint32]protocol.ItemStack{
		3: {Count: 1, ItemType: protocol.ItemType{NetworkID: 777}},
	}
	base.itemNames = map[int32]string{777: "minecraft:shield"}
	return &shieldBot{combatFakeBot: base}
}

// TestRaiseShieldMovesToOffhandAndCrouches: Bedrock resolves blocking off the
// off-hand slot and the sneaking input, and neither alone counts. The old
// raise put the shield in the hotbar and sent a fake use-item transaction,
// which is why the bot could hold a shield all duel and block nothing.
func TestRaiseShieldMovesToOffhandAndCrouches(t *testing.T) {
	t.Parallel()
	f := newShieldTestBot()
	cm := combat.NewCombatManager(f, f.logger)

	if !cm.RaiseShield() {
		t.Fatal("RaiseShield = false with a shield in the bag")
	}
	if f.offhandSlot != 3 {
		t.Fatalf("shield moved from slot %d, want 3 (the slot it was carried in)", f.offhandSlot)
	}
	if !f.sneak {
		t.Fatal("crouch not held; without it the server counts no blocks")
	}

	cm.LowerShield()
	if f.sneak {
		t.Fatal("crouch still held after LowerShield; the body would sneak the whole duel")
	}
}

// TestRaiseShieldWithoutTheOffhandPathIsHonest: a body that cannot move items
// to the off hand must say it failed, not claim a block it cannot make.
func TestRaiseShieldWithoutTheOffhandPathIsHonest(t *testing.T) {
	t.Parallel()
	f := newShieldTestBot()
	f.offhandErr = errFakeEquip
	cm := combat.NewCombatManager(f, f.logger)

	if cm.RaiseShield() {
		t.Fatal("RaiseShield = true although the off-hand move was refused")
	}
	if f.sneak {
		t.Fatal("crouch held although nothing is in the off hand; sneaking without a shield is only a slow walk")
	}
}

// TestPlanShieldDropsTheGuardExactlyForTheSwing: a guard held through the
// attack window blocks everything and hits nothing — the bot that won nothing.
func TestPlanShieldDropsTheGuardExactlyForTheSwing(t *testing.T) {
	t.Parallel()
	inv := map[uint32]string{0: "shield"}

	threatened := combat.Situation{
		TargetDistance: 2, MeleeHostiles: 1,
		Health: 20, MaxHealth: 20,
	}
	if got := combat.PlanShield(inv, threatened, false); got != combat.ShieldRaise {
		t.Fatalf("threatened and healthy: plan = %v, want raise", got)
	}

	swingDue := threatened
	swingDue.SwingReady = true
	if got := combat.PlanShield(inv, swingDue, true); got != combat.ShieldLower {
		t.Fatalf("swing due: plan = %v, want lower — the guard must drop for the hit it is about to make", got)
	}
}

// TestChooseWeaponReachesForTheAxeAgainstArmour: a player who has soaked
// several sword hits without dying is armoured, and armour is the axe's job.
func TestChooseWeaponReachesForTheAxeAgainstArmour(t *testing.T) {
	t.Parallel()
	inv := map[uint32]string{0: "diamond_sword", 1: "diamond_axe"}

	fresh := combat.Situation{PvP: true, TargetDistance: 2, MeleeHostiles: 1, SwingsLanded: 2}
	if got := combat.ChooseWeapon(inv, fresh).Kind; got != combat.WeaponSword {
		t.Fatalf("fresh duel: kind = %v, want sword", got)
	}

	soaked := combat.Situation{PvP: true, TargetDistance: 2, MeleeHostiles: 1, SwingsLanded: 3}
	if got := combat.ChooseWeapon(inv, soaked).Kind; got != combat.WeaponAxe {
		t.Fatalf("three soaked hits: kind = %v, want axe — that is the fair answer to armour", got)
	}

	// Mobs keep the plain sword answer: the doctrine is a duel reading, not a
	// general "swing count" rule.
	mob := combat.Situation{TargetDistance: 2, MeleeHostiles: 1, SwingsLanded: 5}
	if got := combat.ChooseWeapon(inv, mob).Kind; got != combat.WeaponSword {
		t.Fatalf("mob fight: kind = %v, want sword — swing count is only armour evidence against players", got)
	}
}

// TestDamageWithNoPlayerSwingDoesNotBlameNearbyAlly ensures environmental/fall
// damage taken while standing next to an ally never fires a scolding AI event.
func TestDamageWithNoPlayerSwingDoesNotBlameNearbyAlly(t *testing.T) {
	t.Parallel()
	r, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Synterix": {2.0, 64, 0}})
	td.SetAllyFilter(func(name string) bool { return name == "Synterix" })

	// No swing and no SetLastHurtBy: environmental damage near an ally.
	td.OnDamaged(mgl32.Vec3{0, 64, 0})

	if cm.InCombat() {
		t.Fatal("engaged ally over environmental damage")
	}
	if got := r.lastEvent(); got != "" {
		t.Fatalf("blamed nearby ally for non-player damage with event: %q", got)
	}
}
