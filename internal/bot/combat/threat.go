// Package combat handles threat detection and combat logic.
package combat

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
)

var hostileMobs = map[string]bool{
	"zombie":          true,
	"skeleton":        true,
	"creeper":         true,
	"spider":          true,
	"cave_spider":     true,
	"enderman":        true,
	"witch":           true,
	"slime":           true,
	"magma_cube":      true,
	"phantom":         true,
	"drowned":         true,
	"husk":            true,
	"stray":           true,
	"pillager":        true,
	"vindicator":      true,
	"evoker":          true,
	"ravager":         true,
	"blaze":           true,
	"ghast":           true,
	"wither_skeleton": true,
	"piglin_brute":    true,
	"warden":          true,
	"vex":             true,
	"guardian":        true,
	"elder_guardian":  true,
	"silverfish":      true,
	"endermite":       true,
	"hoglin":          true,
	"zoglin":          true,
	"breeze":          true,
	"bogged":          true,
}

type ThreatDetector struct {
	bot            Bot
	combat         *CombatManager
	logger         *slog.Logger
	alertCooldowns map[uint64]time.Time
	mu             sync.Mutex
	// guardMode is the in-game guardian toggle, off by default. With it off a
	// distant hostile only produces an AI alert and the reflex engages at 4
	// blocks; with it on the detector hunts for the mob that most endangers the
	// protected player and engages it out to guardEngageRadius — the difference
	// between "didiemin" and "bunuh aja ya".
	guardMode      bool
	guardProtector string
	// hits is the proportionate-defence ladder: how many times this attacker
	// has hit the bot inside one scuffle, and therefore what they have earned.
	hits HitTracker
	// lastSwingID and lastSwingAt are the most recent arm swing observed from
	// another player. A swing is the evidence that damage came from a person
	// rather than from a fall, a cactus or a mob — the bot used to guess that
	// from "a player happens to be nearby", which is how it ended up attacking
	// its owner over fall damage.
	lastSwingID uint64
	lastSwingAt time.Time
	// lastHurtByPlayerAt is when the server's SetLastHurtBy said a player dealt
	// the damage. It carries no runtime ID, so it corroborates an attribution
	// rather than naming one.
	lastHurtByPlayerAt time.Time
	// isAlly marks players the bot never fights: the ones it answers to and
	// the ones they granted access to. Nil means nobody is an ally.
	isAlly func(name string) bool
}

func NewThreatDetector(bot Bot, cm *CombatManager, logger *slog.Logger) *ThreatDetector {
	return &ThreatDetector{
		bot:            bot,
		combat:         cm,
		logger:         logger,
		alertCooldowns: make(map[uint64]time.Time),
	}
}

func (td *ThreatDetector) Scan(ctx context.Context) {
	if td.combat.InCombat() {
		return
	}

	botPos := td.bot.GetCoords()
	entities := td.bot.GetEntities()
	now := time.Now()

	td.cleanupCooldowns(now)

	visible := entity.VisibleMobs(td.bot.GetLocalWorldModel(), td.bot, botPos, entities, 16, nil)
	closestHostile, closestDist, newThreats := td.evaluateThreats(botPos, visible, now)
	if closestHostile == nil {
		return
	}

	if td.guardScan(visible, botPos) {
		return
	}

	td.handleThreat(closestHostile, closestDist, newThreats, botPos)
}

func (td *ThreatDetector) cleanupCooldowns(now time.Time) {
	td.mu.Lock()
	for id, t := range td.alertCooldowns {
		if now.Sub(t) > 30*time.Second {
			delete(td.alertCooldowns, id)
		}
	}
	td.mu.Unlock()
}

func (td *ThreatDetector) evaluateThreats(botPos mgl32.Vec3, entities []*entity.Info, now time.Time) (*entity.Info, float32, []*entity.Info) {
	var closestHostile *entity.Info
	closestDist := float32(math.MaxFloat32)
	var newThreats []*entity.Info

	for _, ent := range entities {
		if !isHostileEntity(ent) {
			continue
		}

		dist := td.distance(botPos, ent.Position)

		if dist < closestDist {
			closestDist = dist
			closestHostile = ent
		}

		td.mu.Lock()
		lastAlert, onCooldown := td.alertCooldowns[ent.ID]
		if !onCooldown || now.Sub(lastAlert) > 2*time.Minute {
			newThreats = append(newThreats, ent)
			td.alertCooldowns[ent.ID] = now
		}
		td.mu.Unlock()
	}

	return closestHostile, closestDist, newThreats
}

// isHostileEntity decides whether an actor is one of the mobs the detector
// reacts to.
//
// The lookup normalises before comparing. The actor's Type is always
// namespaced — "minecraft:zombie" — and an unnamed mob's Name is a copy of
// that same Type (handleAddActor falls back to it), so the raw strings never
// matched the bare keys in hostileMobs and the whole threat path was dead
// code for every mob without a nametag: no reflex engage, no alerts, which is
// the "mob jahat diemin terus" the guard feature had to fight through.
func isHostileEntity(ent *entity.Info) bool {
	return hostileMobs[entity.NormalizeName(ent.Name)] || hostileMobs[entity.NormalizeName(ent.Type)]
}

func (td *ThreatDetector) handleThreat(closestHostile *entity.Info, closestDist float32, newThreats []*entity.Info, botPos mgl32.Vec3) {
	// Reflex Self-Defense: auto-engage if hostile is very close (within 4 blocks)
	if closestDist <= 4.0 {
		td.logger.Info("ThreatDetector: self-defense reflex engage", "name", closestHostile.Name, "dist", closestDist)
		td.combat.EngageTarget(closestHostile.ID)
		return
	}

	// Alert AI and player of nearby threats if close (< 12 blocks) and not on cooldown
	if len(newThreats) > 0 && closestDist <= 12.0 {
		threatsStr := td.formatThreats(botPos, newThreats)
		td.logger.Info("ThreatDetector: alerting player/AI of threats", "threats", threatsStr)

		systemMsg := fmt.Sprintf("[SYSTEM EVENT: Hostile mobs detected nearby: %s. Decide: fight them or warn the player. Use \\attack\\ if you want to fight.]", threatsStr)
		td.bot.InjectAIEvent(systemMsg)
	}
}

// Attribution windows and reach.
const (
	// playerHitReach is how close a player has to be to plausibly be the one
	// who hit the bot. It is slightly past melee reach, because the server
	// reports the attacker's position a tick or two behind the swing.
	playerHitReach float32 = 4.5
	// swingAttributionWindow is how recently a player must have swung for the
	// damage to be read as theirs. One client tick of slack keeps a fast
	// attacker from escaping attribution by swinging between packets.
	swingAttributionWindow = 900 * time.Millisecond
	// lastHurtByWindow is how long the server's SetLastHurtBy verdict stays
	// relevant.
	lastHurtByWindow = 2 * time.Second
)

// nearestTrackedPlayerReader is the optional view of the bot that can look up
// tracked players directly. Players are not actors (handleAddPlayer never
// touches b.Actors), so the reflex needs this read and cannot fall back to
// GetEntities — a bot without it just never retaliates, which is the safe
// default.
type nearestTrackedPlayerReader interface {
	NearestTrackedPlayer(origin mgl32.Vec3, maxDist float32) (name string, id uint64, pos mgl32.Vec3, ok bool)
}

// NotePlayerSwing records an arm swing observed from another player.
//
// A swing is the only packet-level evidence that a human meant to hit
// something, and it is what separates "a player hit me" from "I fell over
// while a player was standing next to me". The runtime ID is kept so the hit
// can be attributed to that specific player rather than to whoever is closest.
func (td *ThreatDetector) NotePlayerSwing(runtimeID uint64) {
	if runtimeID == 0 {
		return
	}
	td.mu.Lock()
	td.lastSwingID = runtimeID
	td.lastSwingAt = time.Now()
	td.mu.Unlock()
}

// NotePlayerDamage records the server's SetLastHurtBy verdict that the damage
// just taken came from a player. It is a corroborating signal, not a naming
// one: the packet carries an entity type, never a username.
func (td *ThreatDetector) NotePlayerDamage() {
	td.mu.Lock()
	td.lastHurtByPlayerAt = time.Now()
	td.mu.Unlock()
}

// SetAllyFilter installs the never-fight list — the linked players the bot
// answers to, and whoever they granted access to. A poke from an ally is a
// conversation, never a fight.
func (td *ThreatDetector) SetAllyFilter(isAlly func(name string) bool) {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.isAlly = isAlly
}

// IsAlly reports whether the player is on the never-fight list.
func (td *ThreatDetector) IsAlly(name string) bool {
	td.mu.Lock()
	filter := td.isAlly
	td.mu.Unlock()
	return filter != nil && filter(name)
}

// hitsFromThisAttacker reports whether the damage just taken can be pinned on
// this player. The nearest player being close is not enough: falls, mobs and
// poison all arrive as the same health drop.
func (td *ThreatDetector) hitsFromThisAttacker(id uint64) bool {
	now := time.Now()
	td.mu.Lock()
	defer td.mu.Unlock()
	if id != 0 && td.lastSwingID == id && !td.lastSwingAt.IsZero() && now.Sub(td.lastSwingAt) <= swingAttributionWindow {
		return true
	}
	return !td.lastHurtByPlayerAt.IsZero() && now.Sub(td.lastHurtByPlayerAt) <= lastHurtByWindow
}

// OnDamaged is the self-defence reflex for player hits.
//
// It answers proportionately, not maximally. One punch earns a reaction, not a
// duel: the bot turns, says something, and keeps its hands to itself. A short
// burst of hits earns a defensive answer — hit back from where it stands, with
// what it already holds, a few swings at most. Only somebody who keeps coming
// earns the ordinary duel doctrine.
//
// Two things must hold before anything is thrown at all. The damage has to be
// attributable to the player: the nearest player being nearby is not evidence,
// because falls, mobs and poison all arrive as the same health drop, and reading
// closeness as guilt is how the bot attacked whoever happened to be standing
// next to it. And the player must not be on the never-fight list — hitting the
// person the bot answers to is never a self-defence reflex, it is a bug.
func (td *ThreatDetector) OnDamaged(botPos mgl32.Vec3) {
	reader, ok := td.bot.(nearestTrackedPlayerReader)
	if !ok {
		return
	}
	attacker, id, attackerPos, found := reader.NearestTrackedPlayer(botPos, playerHitReach)
	if !found {
		return
	}

	// Turn to face whoever did it. Looking at somebody who just hit you is the
	// visible half of the reaction, and it is the only thing a warning-level
	// answer does with the body.
	td.bot.LookAt(attackerPos.Add(mgl32.Vec3{0, 1.2, 0}))

	if td.IsAlly(attacker) {
		td.logger.Info("ThreatDetector: hit by an ally, never fighting back", "player", attacker)
		td.hits.Reset()
		td.bot.InjectAIEvent("[SYSTEM EVENT: " + attacker + " — the player you answer to — just hit you. You are NOT fighting back and must not start. React naturally in chat with ONE short line (mungkin becanda, mungkin protes ringan), no action tags.]")
		return
	}

	if !td.hitsFromThisAttacker(id) {
		td.logger.Info("ThreatDetector: damage had no player behind it, not retaliating",
			"nearest_player", attacker)
		return
	}

	level, hits, fresh := td.hits.Record(attacker, time.Now())
	if fresh {
		// One line per scuffle, before the per-level lines, so the ladder reads
		// in order afterwards without a line for every swing.
		td.logger.Info("ThreatDetector: scuffle started", "player", attacker)
	}
	switch level {
	case RetaliationWarn:
		td.logger.Info("ThreatDetector: first hit, warning only",
			"player", attacker, "hits", hits)
		td.bot.InjectAIEvent("[SYSTEM EVENT: " + attacker + " just hit you once. You are NOT fighting back. React naturally in chat with ONE short line — kaget, protes, atau warning ringan — and keep doing whatever you were doing. No action tags.]")

	case RetaliationDefend:
		td.logger.Info("ThreatDetector: repeated hits, hitting back defensively",
			"player", attacker, "hits", hits)
		if td.combat.EngageDefender(attacker) {
			td.bot.InjectAIEvent("[SYSTEM EVENT: " + attacker + " keeps hitting you. You are hitting back defensively — standing your ground, not chasing. Say ONE short line about it in chat, no action tags.]")
		}

	case RetaliationDuel:
		td.logger.Info("ThreatDetector: sustained attack, engaging properly",
			"player", attacker, "hits", hits)
		td.combat.EngagePlayer(attacker)
		td.bot.InjectAIEvent("[SYSTEM EVENT: " + attacker + " has hit you " + strconv.Itoa(hits) + " times and is not stopping. You are fighting back for real now. Tell the player naturally in chat, briefly.]")

	default:
		return
	}
}

// guardEngageRadius is how far from the protected player (or the bot) a
// hostile may sit and still be engaged on sight in guard mode. Beyond it the
// behaviour falls back to the stock alert, which is the off-mode behaviour.
const guardEngageRadius = 12.0

// SetGuardMode turns the in-game guardian behaviour on or off. The protector is
// the player who asked, and the hostile picked is the one closest to them.
func (td *ThreatDetector) SetGuardMode(enabled bool, protector string) {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.guardMode = enabled
	td.guardProtector = protector
}

// GuardMode reports the current guardian toggle and who it protects.
func (td *ThreatDetector) GuardMode() (bool, string) {
	td.mu.Lock()
	defer td.mu.Unlock()
	return td.guardMode, td.guardProtector
}

// guardScan is the guard-mode half of the scan. It engages the hostile with the
// smallest distance to either the protected player or the bot, whichever is
// nearer, inside guardEngageRadius — so a zombie drifting toward the player is
// attacked while it is still their problem, not only when it reaches the bot.
//
// It reports whether it engaged, and the caller then skips the stock alert
// path: a fight that already started needs no "decide whether to fight" event.
// When the protector is not currently tracked (logged off, out of range) the
// anchor degrades to the bot itself, which is exactly the off-mode reflex
// widened to the guard radius.
func (td *ThreatDetector) guardScan(visible []*entity.Info, botPos mgl32.Vec3) bool {
	enabled, protector := td.GuardMode()
	if !enabled {
		return false
	}

	anchor := botPos
	anchorName := "bot"
	if _, pos, ok := td.bot.FindPlayer(protector); ok {
		anchor = pos
		anchorName = protector
	}

	var best *entity.Info
	bestAnchorDist := float32(math.MaxFloat32)
	for _, ent := range visible {
		if !isHostileEntity(ent) {
			continue
		}
		toAnchor := td.distance(anchor, ent.Position)
		if toAnchor < bestAnchorDist {
			bestAnchorDist = toAnchor
			best = ent
		}
	}
	if best == nil || bestAnchorDist > guardEngageRadius {
		return false
	}

	td.logger.Info("ThreatDetector: guard engage",
		"name", best.Name, "id", best.ID,
		"dist_to_protector", bestAnchorDist, "protector", anchorName)
	td.combat.EngageTarget(best.ID)
	return true
}

func (td *ThreatDetector) formatThreats(botPos mgl32.Vec3, threats []*entity.Info) string {
	list := make([]string, 0, len(threats))
	for _, t := range threats {
		list = append(list, fmt.Sprintf("%s (dist: %.0f block)", t.Name, td.distance(botPos, t.Position)))
	}
	return strings.Join(list, ", ")
}

func (td *ThreatDetector) distance(a, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
