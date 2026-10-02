package interact

import (
	"math"
	"sort"
	"strings"

	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// People do not say "interact with the nearest oak door entity", they say
// "klik pintu di depanku". Each canonical concept below carries the words that
// mean it — English and Indonesian, because the bot's users speak both and the
// model may echo either back.
//
// The canonical key is what gets matched against the world, so a request for
// "pintu" resolves to the English "door" the block is actually named.
var (
	entityConcepts = map[string][]string{
		"npc":         {"npc", "figure", "figur", "character", "karakter", "dialog"},
		"person":      {"person", "player", "pemain", "orang", "manusia"},
		"join":        {"join", "server", "button", "tombol", "menu", "portal", "lobby", "hub"},
		"villager":    {"villager"},
		"zombie":      {"zombie"},
		"skeleton":    {"skeleton", "skel"},
		"creeper":     {"creeper"},
		"spider":      {"spider"},
		"enderman":    {"enderman"},
		"golem":       {"golem", "iron golem", "snow golem"},
		"armor_stand": {"armor stand", "armor_stand", "mannequin", "dummy", "patung"},
		"animal":      {"animal", "mob", "hewan", "binatang", "creature"},
		"minecart":    {"minecart", "mine cart", "tambang"},
		"boat":        {"boat", "perahu"},
		"enderchest":  {"ender chest", "ender_chest"},
	}

	blockConcepts = map[string][]string{
		"sign":             {"sign", "papan", "notice", "plang"},
		"door":             {"door", "pintu"},
		"trapdoor":         {"trapdoor", "trap door", "pintu jebakan", "jebakan"},
		"button":           {"button", "tombol"},
		"lever":            {"lever", "tuas", "pengungkit"},
		"gate":             {"fence gate", "gate", "gerbang", "pagar"},
		"chest":            {"chest", "peti", "lemari"},
		"barrel":           {"barrel", "drum", "tong"},
		"ladder":           {"ladder", "tangga"},
		"pressure_plate":   {"pressure plate", "plate", "piring", "tekan"},
		"anvil":            {"anvil", "engkapa"},
		"crafting_table":   {"crafting table", "meja kerja", "meja crafting"},
		"furnace":          {"furnace", "tungku", "peluru"},
		"brewing_stand":    {"brewing stand", "tempat ramu"},
		"bell":             {"bell", "lonceng"},
		"campfire":         {"campfire", "api unggun", "tungku api"},
		"enchanting_table": {"enchanting table", "meja enchant"},
		"lectern":          {"lectern", "podium", "kursi baca"},
		"hopper":           {"hopper", "corong"},
		"dispenser":        {"dispenser", "pembatas", "dropper"},
		"beacon":           {"beacon", "menara cahaya"},
		"respawn_anchor":   {"respawn anchor", "jangkar"},
		"smithing_table":   {"smithing table", "meja tempa"},
		"loom":             {"loom", "mesin tenun"},
		"stonecutter":      {"stonecutter", "pemotong batu"},
		"grindstone":       {"grindstone", "batu asah"},
		"smithingtable":    {"smithing table"},
		"crafter":          {"crafter", "pembuat"},
		"vault":            {"vault", "lemari bawah tanah"},
		"decorated_pot":    {"decorated pot", "pot", "mangkuk"},
		"flower_pot":       {"flower pot", "pot bunga"},
		"jukebox":          {"jukebox", "kotak musik"},
		"note_block":       {"note block", "blok nota"},
	}

	// frontWords ask for "whatever I am looking at" rather than naming a thing.
	frontWords = []string{
		"depanmu", "didepanmu", "di depanmu", "depan", "didepan", "di depan",
		"in front", "infront", "front", "nearest", "terdekat", "closest",
		"yang itu", "yg itu", "itu", "sana", "disana", "there",
	}
)

// interactiveBlockNames are the block families that have a real interaction.
// Anything not in this list is skipped when scanning, so "click the thing in
// front" can never land on a plain wall block.
var interactiveBlockNames = []string{
	"door", "trapdoor", "button", "lever", "gate", "chest", "barrel", "ladder",
	"pressure_plate", "anvil", "crafting_table", "furnace", "brewing_stand",
	"bell", "campfire", "enchanting_table", "lectern", "hopper", "dispenser",
	"dropper", "beacon", "respawn_anchor", "smithing_table", "loom",
	"stonecutter", "grindstone", "crafter", "vault", "decorated_pot",
	"flower_pot", "jukebox", "note_block", "sign",
}

// stateChangingFamilies are interactive blocks whose activation visibly
// changes the block itself. Clicking one of these is verifiable: if the
// network ID at the position did not change, the click did not land. UI blocks
// (chest, crafting table, sign) open a screen instead and leave nothing to
// observe, and iron doors/trapdoors do not move on click at all.
var stateChangingFamilies = []string{
	"button", "lever", "door", "trapdoor", "fence_gate", "gate",
}

// activationChangesState reports whether clicking the named block should
// change its block state, which decides whether a click can be verified by
// watching the block's network ID.
func activationChangesState(name string) bool {
	n := Normalise(name)
	if strings.Contains(n, "iron_door") || strings.Contains(n, "iron_trapdoor") {
		return false
	}
	for _, family := range stateChangingFamilies {
		if strings.Contains(n, family) {
			return true
		}
	}
	return false
}

// ParseRequest pulls a target out of the free-text parameter of an interact
// action.
//
// Some words are genuinely ambiguous and both readings get filled in. "tombol"
// is the clear case: on one hub it is a stone button block, on the next it is an
// armour stand named "Join Server". ParseRequest does not guess — Resolve tries
// the block reading first and falls back to the entity, using what actually
// exists nearby as the tie-breaker.
//
// A word that names something always wins over the vague "in front of you", so
// "klik sign di depan" targets a sign rather than whatever is closest to the
// camera.
func ParseRequest(param string) Request {
	clean := Normalise(param)
	request := Request{Raw: param}

	if canonical, ok := matchConcept(clean, blockConcepts); ok {
		request.Block = canonical
	}
	if canonical, ok := matchConcept(clean, entityConcepts); ok {
		request.Entity = canonical
	}
	if request.Block == "" && request.Entity == "" {
		if clean == "" || containsAny(clean, frontWords) {
			request.InFront = true
			return request
		}
		// Nothing matched: treat the whole thing as a player's name, which is how
		// "interact:Arthenyxx" is meant to read.
		request.Entity = clean
	}
	return request
}

// matchConcept finds the concept whose aliases appear in the text. Concepts are
// checked longest-alias-first so "iron golem" wins over "golem" and "fence gate"
// over "gate".
func matchConcept(text string, concepts map[string][]string) (string, bool) {
	type hit struct {
		canonical string
		length    int
	}
	var hits []hit
	for canonical, aliases := range concepts {
		for _, alias := range aliases {
			if len(alias) < 3 {
				continue
			}
			if strings.Contains(text, alias) {
				hits = append(hits, hit{canonical, len(alias)})
			}
		}
	}
	if len(hits) == 0 {
		return "", false
	}
	sort.Slice(hits, func(a, c int) bool {
		if hits[a].length != hits[c].length {
			return hits[a].length > hits[c].length
		}
		return hits[a].canonical < hits[c].canonical
	})
	return hits[0].canonical, true
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// EntityNameMatches reports whether an entity is what the request named. Both
// the entity type and its display name are checked, because a hub's join button
// is usually an armour stand or NPC with a display name like "Join Server"
// rather than anything the type name would reveal.
func EntityNameMatches(ent *entity.Info, canonical string) bool {
	if ent == nil {
		return false
	}
	aliases, ok := entityConcepts[canonical]
	if !ok {
		aliases = []string{canonical}
	}
	return containsAny(Normalise(ent.Type), aliases) || containsAny(Normalise(ent.Name), aliases)
}

// BlockNameMatches reports whether a block is what the request named.
func BlockNameMatches(blockName, canonical string) bool {
	aliases, ok := blockConcepts[canonical]
	if !ok {
		aliases = []string{canonical}
	}
	return containsAny(Normalise(blockName), aliases)
}

// IsInteractiveBlockName reports whether a block has an interaction worth
// clicking. Signs are included: clicking one is how a player reads it, even
// though the text itself arrives out of band.
func IsInteractiveBlockName(blockName string) bool {
	return containsAny(Normalise(blockName), interactiveBlockNames)
}

// IsNPCType reports whether an entity should also get an NPC dialogue request.
func IsNPCType(entityType string) bool {
	name := Normalise(entityType)
	return strings.Contains(name, "npc") || strings.Contains(name, "education")
}

// isInteractableEntity filters out the things that are not click targets:
// dropped items (which the looter handles) and the bot itself.
func isInteractableEntity(ent *entity.Info) bool {
	if ent == nil {
		return false
	}
	name := Normalise(ent.Type)
	if name == "item" || strings.Contains(name, "item_entity") {
		return false
	}
	return true
}

// ForwardVector converts a body yaw into a horizontal direction.
//
// The project convention is yaw = atan2(dz, dx) * 180/π − 90, so the inverse
// adds the 90° back before taking the sine and cosine. Getting this backwards
// points "in front of you" at whatever is behind you.
func ForwardVector(yaw float32) mgl32.Vec3 {
	rad := float64(yaw+90) * math.Pi / 180
	return mgl32.Vec3{float32(math.Cos(rad)), 0, float32(math.Sin(rad))}.Normalize()
}

// WithinCone reports whether a position sits inside a cone around forward.
func WithinCone(from, to mgl32.Vec3, forward mgl32.Vec3, coneDegrees float32) bool {
	dx := to.X() - from.X()
	dz := to.Z() - from.Z()
	length := float32(math.Hypot(float64(dx), float64(dz)))
	if length < 0.01 {
		// Standing on top of it counts as "in front"; there is no direction to
		// compare against.
		return true
	}
	dot := (dx/len3(dx, dz))*forward.X() + (dz/len3(dx, dz))*forward.Z()
	// Clamp guards against a NaN creeping in from a zero-length forward vector.
	dot = clampUnit(dot)
	angle := math.Acos(float64(dot)) * 180 / math.Pi
	return float32(angle) <= coneDegrees
}

func len3(dx, dz float32) float32 {
	return float32(math.Sqrt(float64(dx*dx + dz*dz)))
}

func clampUnit(v float32) float32 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

// BlockFaceToward returns the block face pointing back at the bot, which is the
// face a client reports clicking. Sending the wrong face is silently ignored, so
// this is picked from the dominant axis rather than guessed.
//
// Faces: 0 down, 1 up, 2 north (−Z), 3 south (+Z), 4 west (−X), 5 east (+X).
func BlockFaceToward(pos protocol.BlockPos, from mgl32.Vec3) int32 {
	dx := from.X() - (float32(pos.X()) + 0.5)
	dy := from.Y() - (float32(pos.Y()) + 0.5)
	dz := from.Z() - (float32(pos.Z()) + 0.5)

	absX := Abs32(dx)
	absY := Abs32(dy)
	absZ := Abs32(dz)

	switch {
	case absY > absX && absY > absZ:
		if dy > 0 {
			return 1 // standing above it: click the top
		}
		return 0
	case absZ > absX:
		if dz > 0 {
			return 3 // south
		}
		return 2 // north
	default:
		if dx > 0 {
			return 5 // east
		}
		return 4 // west
	}
}

func Abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// aimPoint is where the head should point for an entity: a little above the
// feet, inside the entity's hitbox.
func aimPoint(feet mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{feet.X(), feet.Y() + 1.0, feet.Z()}
}

func horizontalDistance(from, to mgl32.Vec3) float32 {
	return float32(math.Hypot(float64(to.X()-from.X()), float64(to.Z()-from.Z())))
}

// inReach uses the same 3D check for the entity limit, since an entity on a
// ledge above the bot is still clickable.
func inReach(from, to mgl32.Vec3, reach float32) bool {
	dx := to.X() - from.X()
	dy := to.Y() - from.Y()
	dz := to.Z() - from.Z()
	return float32(math.Sqrt(float64(dx*dx+dy*dy+dz*dz))) <= reach
}
