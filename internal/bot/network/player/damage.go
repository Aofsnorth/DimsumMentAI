package player

import (
	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Damage attribution.
//
// The self-defence reflex needs to know whether the damage it just took came
// from a player or from the world. On this protocol there is no damage packet
// carrying a source runtime ID, so the evidence has to be assembled from the
// two signals that do exist:
//
//   - Animate: a player swinging their arm. That is the input a hit comes
//     from, and it names the entity that swung.
//   - SetLastHurtBy: the server's own verdict on what hurt the bot. It carries
//     an entity type rather than a runtime ID, so it can corroborate but not
//     name.
//
// Reading "the nearest player" as the attacker — which is what the reflex used
// to do — is how fall damage next to the owner turned into a duel against the
// owner.

// entityTypePlayer is Bedrock's numeric actor type for a player. Players are
// spawned with AddPlayer rather than AddActor, so this number never appears in
// the bot's own entity tracking and has to be stated here.
const entityTypePlayer = 319

// handleAnimate notes arm swings from other players. Only an attack swing
// counts as evidence of a hit: a player mining or building next to the bot is
// working, not fighting.
func handleAnimate(b *bot.Bot, pk packet.Packet) bool {
	p, ok := pk.(*packet.Animate)
	if !ok {
		return false
	}
	if p.ActionType != packet.AnimateActionSwingArm {
		return true
	}
	if b.Conn == nil || p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID {
		return true
	}
	if p.SwingSource != 0 && p.SwingSource != packet.AnimateSwingSourceAttack {
		return true
	}
	b.Mu.Lock()
	_, isTrackedPlayer := b.PlayerUsernames[p.EntityRuntimeID]
	b.Mu.Unlock()
	if !isTrackedPlayer {
		return true
	}
	if b.ThreatDet != nil {
		b.ThreatDet.NotePlayerSwing(p.EntityRuntimeID)
	}
	return true
}

// handleSetLastHurtBy records the server saying a player dealt the last damage.
func handleSetLastHurtBy(b *bot.Bot, pk packet.Packet) bool {
	p, ok := pk.(*packet.SetLastHurtBy)
	if !ok {
		return false
	}
	if p.EntityType == entityTypePlayer && b.ThreatDet != nil {
		b.ThreatDet.NotePlayerDamage()
	}
	return true
}
