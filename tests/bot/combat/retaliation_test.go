// The defensive answer, from the player's seat: hitting the bot a couple of
// times gets a hit back, not a pursuit. These are the behaviours that made the
// old reflex read as "agresif banget": the chase, the weapon draw, and the
// unlimited swings.
package combat_test

import (
	"context"
	"testing"
	"time"

	"bedrock-ai/internal/bot/combat"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// attackTransactions counts the melee attack packets the fake received, which
// is how these tests read how many swings actually went out.
func (f *combatFakeBot) attackTransactions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, pk := range f.packets {
		tx, ok := pk.(*packet.InventoryTransaction)
		if !ok {
			continue
		}
		if _, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData); ok {
			n++
		}
	}
	return n
}

// TestDefensiveAnswerDoesNotChase: a defender stands their ground. The moment
// the attacker backs out of the leash the answer is over, because following
// them is the aggressive half of a fight.
func TestDefensiveAnswerDoesNotChase(t *testing.T) {
	t.Parallel()
	r, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Attacker": {2.0, 64, 0}})
	id := r.ids["Attacker"]

	hitOnce(td, id, mgl32.Vec3{0, 64, 0})
	hitOnce(td, id, mgl32.Vec3{0, 64, 0})
	if got := cm.ModeForTest(); got != combat.ModeDefend {
		t.Fatalf("mode = %v, want defend before the chase check", got)
	}

	// A few ticks in reach first: the answer must not walk anywhere even while
	// the attacker is standing right there.
	navigationsBefore := r.navigateCalls()
	for i := 0; i < 3; i++ {
		cm.Tick(context.TODO())
	}
	if got := r.navigateCalls() - navigationsBefore; got != 0 {
		t.Fatalf("defensive answer issued %d walk orders, want 0 — a defender does not chase", got)
	}

	// The attacker runs. The bot must not follow.
	far := mgl32.Vec3{7.0, 64, 0}
	r.mu.Lock()
	r.tracked["Attacker"] = far
	r.mu.Unlock()
	r.combatFakeBot.mu.Lock()
	r.combatFakeBot.players["Attacker"] = struct {
		id  uint64
		pos mgl32.Vec3
	}{id: id, pos: far}
	r.combatFakeBot.mu.Unlock()

	cm.Tick(context.TODO())

	if cm.InCombat() {
		t.Fatal("kept engaging a player who walked away — that is a chase, not a defence")
	}
}

// TestDefensiveAnswerStopsAfterEnoughSwings: hit back enough to make the point,
// then stop. A defender who keeps swinging is farming the attacker.
func TestDefensiveAnswerStopsAfterEnoughSwings(t *testing.T) {
	t.Parallel()
	r, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Attacker": {2.0, 64, 0}})
	id := r.ids["Attacker"]

	hitOnce(td, id, mgl32.Vec3{0, 64, 0})
	hitOnce(td, id, mgl32.Vec3{0, 64, 0})

	for i := 0; i < 6; i++ {
		cm.Tick(context.TODO())
		cm.SetLastAttackForTest(time.Now().Add(-time.Second))
	}

	if got := r.attackTransactions(); got > 3 {
		t.Fatalf("defensive answer landed %d swings, want at most 3", got)
	}
	if cm.InCombat() {
		t.Fatal("still fighting after the defensive answer was spent")
	}
}

// TestHittingAgainDoesNotRefillTheSwingBudget: the cap is per answer, not per
// punch. Otherwise a player hitting on a rhythm refunds three swings every
// time and the "hit back a little" promise is worth nothing.
func TestHittingAgainDoesNotRefillTheSwingBudget(t *testing.T) {
	t.Parallel()
	r, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Attacker": {2.0, 64, 0}})
	id := r.ids["Attacker"]

	hitOnce(td, id, mgl32.Vec3{0, 64, 0})
	hitOnce(td, id, mgl32.Vec3{0, 64, 0})

	for i := 0; i < 2; i++ {
		cm.Tick(context.TODO())
		cm.SetLastAttackForTest(time.Now().Add(-time.Second))
	}

	// Third hit, still inside the same answer: extends the window, not the
	// budget.
	hitOnce(td, id, mgl32.Vec3{0, 64, 0})
	cm.Tick(context.TODO())
	cm.SetLastAttackForTest(time.Now().Add(-time.Second))
	cm.Tick(context.TODO())

	if got := r.attackTransactions(); got > 3 {
		t.Fatalf("a re-hit refunded the swing budget: %d swings, want at most 3", got)
	}
	if cm.InCombat() {
		t.Fatal("kept fighting after the defensive answer was spent")
	}
}

// TestDefensiveAnswerNeverDrawsAWeapon: a poke is answered with what is in the
// hand. Swapping to a sword is the escalation the user called aggressive.
func TestDefensiveAnswerNeverDrawsAWeapon(t *testing.T) {
	t.Parallel()
	r, cm, td := newRetaliatingBot(map[string]mgl32.Vec3{"Attacker": {2.0, 64, 0}})
	// A sword in the bag: the duel doctrine would equip it.
	r.combatFakeBot.mu.Lock()
	r.combatFakeBot.inventory = map[uint32]protocol.ItemStack{3: {ItemType: protocol.ItemType{NetworkID: 300}, Count: 1}}
	r.combatFakeBot.itemNames = map[int32]string{300: "minecraft:iron_sword"}
	r.combatFakeBot.mu.Unlock()

	id := r.ids["Attacker"]
	hitOnce(td, id, mgl32.Vec3{0, 64, 0})
	hitOnce(td, id, mgl32.Vec3{0, 64, 0})

	for i := 0; i < 4; i++ {
		cm.Tick(context.TODO())
		cm.SetLastAttackForTest(time.Now().Add(-time.Second))
	}

	if got := r.equipCalls(); got != 0 {
		t.Fatalf("defensive answer equipped %d times, want 0 — defending uses what is in hand", got)
	}
}
