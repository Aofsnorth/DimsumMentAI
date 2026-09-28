// Package interact implements low-level world interaction: clicking entities
// (players, NPCs, server buttons and other "figures") and block entities
// (doors, buttons, levers, chests, signs).
//
// Interacting in Bedrock is three different wire protocols depending on what is
// being clicked, and sending the wrong one is silently dropped by the server —
// the packet goes out, nothing happens, and the bot just looks broken:
//
//   - entities: packet.Interact with InteractActionMouseOverEntity, then
//     InteractActionNPCOpen
//   - Education NPCs: packet.NPCRequest
//   - blocks: PlayerActionStartItemUseOn, an InventoryTransaction carrying
//     UseItemTransactionData, then PlayerActionStopItemUseOn
//
// The bot already had the block-placement flavour of the third one, which
// places and breaks blocks; nothing here touches the world model or the
// inventory, so an interaction is safe to retry.
package interact

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/movement/animation"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot is the slice of the bot this package needs. Keeping it narrow makes the
// targeting policy testable without a live connection.
type Bot interface {
	GetCoords() mgl32.Vec3
	GetYaw() float32
	GetEntities() map[uint64]*entity.Info
	FindPlayer(username string) (uint64, mgl32.Vec3, bool)
	GetBlockName(x, y, z int32) (string, bool)
	LookAt(pos mgl32.Vec3)
	WritePacket(pk packet.Packet) error
	GetEntityRuntimeID() uint64
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	ReportActionStatus(user string, status event.ActionStatus)
	ResetLook()
	// GetBlockNetworkID returns the wire-format ID the server assigned to the
	// block at a position. Vanilla clients echo it back in UseItem transactions.
	GetBlockNetworkID(x, y, z int32) (uint32, bool)
	// GetLastSentAim reports the aim carried by the most recent
	// PlayerAuthInput — the direction the server currently believes the bot
	// is looking.
	GetLastSentAim() (yaw, pitch float32)
	// QueueItemInteractionData embeds a use-item transaction in the next
	// PlayerAuthInput, the way 1.21.100+ touch clients send block clicks.
	QueueItemInteractionData(data protocol.UseItemTransactionData) error
}

const (
	// entityReach is how far the bot will click an entity. Bedrock's own
	// interaction range is about this, and beyond it the packet is wasted.
	entityReach = 4.5

	// blockReach is the reach for block entities. Slightly shorter than an
	// entity click because the face has to be resolved, and a wrong face is
	// ignored by the server.
	blockReach = 4.0

	// frontConeDegrees is how wide "in front of you" is. Wide enough that a
	// player aiming roughly at a button need not be pixel-perfect, narrow
	// enough that two buttons side by side stay distinguishable.
	frontConeDegrees = 55.0

	// searchRadius is how far around the bot a named block is looked for.
	searchRadius = 6

	// settleDelay is the pause between the click and its inline fallback, and
	// between aiming and interacting for entity clicks.
	settleDelay = 120 * time.Millisecond

	// eyeHeight is the camera offset above the feet. PlayerAuthInput carries
	// the eye position, so the click transaction must too, or the two views of
	// where the player is disagree.
	eyeHeight = 1.62

	// aimTimeout bounds how long a block click waits for the sent aim to turn
	// toward the target before clicking anyway.
	aimTimeout = 600 * time.Millisecond
	// aimPollInterval is how often the sent aim is sampled while waiting.
	aimPollInterval = 40 * time.Millisecond
	// aimYawTolerance/aimPitchTolerance is how far off the crosshair may be
	// before the click is considered mis-aimed.
	aimYawTolerance   float32 = 12
	aimPitchTolerance float32 = 10
	// statePollTimeout/statePollInterval bound the watch for the clicked
	// block's network ID to change (button pressed, door swung).
	statePollInterval = 40 * time.Millisecond
	statePollTimeout  = 400 * time.Millisecond
)

// Kind is what kind of thing is being interacted with.
type Kind int

const (
	// KindEntity covers players, mobs, NPCs, and server buttons/figures, which
	// are all entities on the wire.
	KindEntity Kind = iota
	// KindBlock covers block entities: doors, buttons, levers, chests, signs.
	KindBlock
)

// Target is a resolved interaction target, ready to be clicked.
type Target struct {
	Kind  Kind
	ID    uint64 // entity runtime ID, meaningless for blocks
	Name  string
	Pos   mgl32.Vec3
	Block protocol.BlockPos
	Face  int32 // block face to click, pointing back at the bot
}

func (t Target) String() string {
	if t.Kind == KindBlock {
		return t.Name + " @ " + blockKey(t.Block)
	}
	return t.Name + " (entity " + strconv.FormatUint(t.ID, 10) + ")"
}

// Request is a parsed interact instruction: what the player asked for, in the
// loose vocabulary people actually use.
type Request struct {
	Raw     string
	Entity  string // an entity type or a player name, if one was named
	Block   string // a block type, if one was named
	InFront bool   // "in front of you" or nothing named at all
}

// Interactor performs world interaction on behalf of the bot.
type Interactor struct {
	bot    Bot
	logger *slog.Logger
}

// New builds an Interactor.
func New(bot Bot, logger *slog.Logger) *Interactor {
	return &Interactor{bot: bot, logger: logger}
}

// Interact resolves the request and clicks whatever it points at.
func (i *Interactor) Interact(ctx context.Context, user, param string) {
	request := ParseRequest(param)
	target, err := i.Resolve(request)
	if err != nil {
		i.logger.Info("interact target not found", "param", param, "error", err.Error())
		i.bot.ReportActionStatus(user, event.ActionStatus{
			Action:  "interact",
			Item:    describeRequest(request),
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	i.logger.Info("interacting",
		"target", target.String(),
		"kind", kindName(target.Kind),
		"param", param,
	)

	ok := false
	failReason := "interupsi: klik tidak selesai"
	if target.Kind == KindBlock {
		ok, failReason = i.interactBlock(ctx, target)
	} else if i.interactEntity(ctx, target) {
		ok = true
	}
	i.bot.ResetLook()

	if !ok {
		// The sequence was cut short, or the block never reacted. Reporting
		// success here would tell the LLM the button was pressed when it was
		// not, and the player would watch a silent button.
		i.bot.ReportActionStatus(user, event.ActionStatus{
			Action:  "interact",
			Item:    target.Name,
			Success: false,
			Error:   failReason,
		})
		return
	}

	i.bot.ReportActionStatus(user, event.ActionStatus{
		Action:  "interact",
		Item:    target.Name,
		Success: true,
	})
}

// interactEntity clicks an entity: hover it, then open it. It reports whether
// the full sequence ran; a cancelled context leaves it half-done and the caller
// must not report success.
//
// MouseOverEntity is what a touch client sends while the finger is down and is
// what actually triggers most server buttons; NPCOpen is the explicit "open
// this NPC" action. Sending both covers the two shapes servers expect instead
// of guessing which one this particular server implements.
func (i *Interactor) interactEntity(ctx context.Context, target Target) bool {
	i.bot.LookAt(target.Pos)
	if !sleepContext(ctx, settleDelay) {
		return false
	}

	_ = i.bot.WritePacket(&packet.Interact{
		ActionType:            packet.InteractActionMouseOverEntity,
		TargetEntityRuntimeID: target.ID,
		Position:              protocol.Option(target.Pos),
	})

	// Same arm movement a player makes when tapping an NPC or server figure.
	_ = i.bot.WritePacket(animation.InteractSwing(i.bot.GetEntityRuntimeID()))

	if !sleepContext(ctx, settleDelay) {
		return false
	}

	_ = i.bot.WritePacket(&packet.Interact{
		ActionType:            packet.InteractActionNPCOpen,
		TargetEntityRuntimeID: target.ID,
	})

	// NPC dialogue exists only on Education Edition worlds and the request is
	// ignored everywhere else. Sending it alongside the Interact is free and
	// makes real NPC figures respond instead of just being clicked at.
	if IsNPCType(target.Name) {
		_ = i.bot.WritePacket(&packet.NPCRequest{
			EntityRuntimeID: target.ID,
			RequestType:     packet.NPCRequestActionExecuteAction,
		})
	}
	return true
}

// interactBlock clicks a block entity the way a vanilla client does: aim,
// swing, one UseItem ClickBlock transaction — then, if the block did not
// visibly react, the same click again through the inline PlayerAuthInput path.
// It returns whether the click landed and, when it did not, a player-readable
// reason.
//
// Two transports because hosts disagree about what a 1.21.100+ touch client
// is allowed to do. The standalone InventoryTransaction is the classic shape
// and the one this bot's block placement is proven with on the same server.
// The inline ItemInteractionData — queued into the next PlayerAuthInput with
// the PerformItemInteraction flag — is what a stock touch client has actually
// sent since 1.21.90, and some hosts only process that one. A click that lands
// through the first path is detected by the block's network ID changing, so
// the fallback never fires and a door does not toggle twice.
//
// The transaction fields mirror the vanilla client: Position is the eye
// position — the same feet+1.62 the PlayerAuthInput heartbeat carries, so the
// two never disagree — and BlockRuntimeID echoes the clicked block's network
// ID, which hosts use to verify the client world is in sync. A zero
// BlockRuntimeID reads as "clicked an unknown block" and is the most likely
// reason earlier button clicks were silently dropped.
func (i *Interactor) interactBlock(ctx context.Context, target Target) (bool, string) {
	if err := ctx.Err(); err != nil {
		return false, "interupsi: klik tidak selesai"
	}

	before, beforeOK := i.bot.GetBlockNetworkID(target.Block.X(), target.Block.Y(), target.Block.Z())
	changesState := activationChangesState(target.Name)

	i.bot.LookAt(target.Pos)
	if !i.waitForAim(ctx, target.Pos) {
		return false, "interupsi: klik tidak selesai"
	}

	// The hand swings as the click lands — a client that presses a button
	// without moving its arm is exactly the bot tell this fixes.
	_ = i.bot.WritePacket(animation.InteractSwing(i.bot.GetEntityRuntimeID()))

	tx := i.clickTransaction(target)
	_ = i.bot.WritePacket(&packet.InventoryTransaction{TransactionData: &tx})

	if !changesState {
		// Chests, signs, crafting tables: activation opens a screen and the
		// block itself never changes, so there is nothing to observe. Queue the
		// inline fallback for hosts that only accept that path, then be done.
		if !sleepContext(ctx, settleDelay) {
			return false, "interupsi: klik tidak selesai"
		}
		_ = i.bot.WritePacket(animation.InteractSwing(i.bot.GetEntityRuntimeID()))
		_ = i.bot.QueueItemInteractionData(tx)
		return true, ""
	}
	if i.waitForStateChange(ctx, target, before, beforeOK) {
		return true, ""
	}

	// The block never moved: retry once through the inline path.
	if !sleepContext(ctx, settleDelay) {
		return false, "interupsi: klik tidak selesai"
	}
	_ = i.bot.WritePacket(animation.InteractSwing(i.bot.GetEntityRuntimeID()))
	if err := i.bot.QueueItemInteractionData(tx); err != nil {
		// Another interaction is already queued; the standalone click still
		// went out, so report what is known rather than claiming a failure.
		return true, ""
	}
	if i.waitForStateChange(ctx, target, before, beforeOK) {
		return true, ""
	}
	return false, target.Name + " tidak merespons klik"
}

// waitForAim blocks until the aim in the most recent PlayerAuthInput points at
// the target. LookAt snaps the yaw and pitch directly rather than easing them,
// so the first heartbeat after the call normally satisfies this; the timeout
// keeps a stalled input loop from wedging the click forever, and on timeout
// the click is sent anyway — hosts do not gate block clicks on the crosshair.
func (i *Interactor) waitForAim(ctx context.Context, pos mgl32.Vec3) bool {
	eye := i.bot.GetCoords().Add(mgl32.Vec3{0, eyeHeight, 0})
	wantYaw, wantPitch := aimAngles(eye, pos)
	deadline := time.Now().Add(aimTimeout)
	for {
		yaw, pitch := i.bot.GetLastSentAim()
		if angleDelta(yaw, wantYaw) <= aimYawTolerance && angleDelta(pitch, wantPitch) <= aimPitchTolerance {
			return true
		}
		if !sleepContext(ctx, aimPollInterval) {
			return false
		}
		if time.Now().After(deadline) {
			return true
		}
	}
}

// aimAngles converts a direction into the yaw/pitch pair the bot sends in
// PlayerAuthInput: yaw 0 faces +Z, 90 faces −X; pitch is negative looking up.
// It is the inverse of ForwardVector and matches movement.LookAt.
func aimAngles(from, to mgl32.Vec3) (yaw, pitch float32) {
	d := to.Sub(from)
	distH := math.Sqrt(float64(d.X()*d.X() + d.Z()*d.Z()))
	if distH < 0.001 {
		distH = 0.001
	}
	yaw = float32(math.Atan2(float64(-d.X()), float64(d.Z())) * 180 / math.Pi)
	if yaw < 0 {
		yaw += 360
	}
	pitch = float32(-math.Atan2(float64(d.Y()), distH) * 180 / math.Pi)
	if pitch > 90 {
		pitch = 90
	} else if pitch < -90 {
		pitch = -90
	}
	return yaw, pitch
}

// angleDelta returns the absolute difference between two angles in degrees,
// treating 359 and 1 as 2 degrees apart.
func angleDelta(a, b float32) float32 {
	d := float32(math.Abs(float64(a - b)))
	for d >= 360 {
		d -= 360
	}
	if d > 180 {
		d = 360 - d
	}
	return d
}

// clickTransaction builds the UseItem ClickBlock payload shared by both
// transports, copying the field shapes a vanilla client sends.
func (i *Interactor) clickTransaction(target Target) protocol.UseItemTransactionData {
	held, hotbar := i.heldItem()
	tx := protocol.UseItemTransactionData{
		ActionType:      protocol.UseItemActionClickBlock,
		TriggerType:     protocol.TriggerTypePlayerInput,
		BlockPosition:   target.Block,
		BlockFace:       target.Face,
		HotBarSlot:      hotbar,
		HeldItem:        held,
		Position:        i.bot.GetCoords().Add(mgl32.Vec3{0, eyeHeight, 0}),
		ClickedPosition: faceClickedPosition(target.Face),
	}
	if rid, ok := i.bot.GetBlockNetworkID(target.Block.X(), target.Block.Y(), target.Block.Z()); ok {
		tx.BlockRuntimeID = rid
	}
	return tx
}

// waitForStateChange polls the clicked block's network ID until it differs
// from the value captured before the click — a pressed button, a swung door —
// or the timeout passes. False means "nothing changed", which the caller
// treats as the click not landing and retries through another transport.
func (i *Interactor) waitForStateChange(ctx context.Context, target Target, before uint32, beforeOK bool) bool {
	if !beforeOK {
		return false
	}
	deadline := time.Now().Add(statePollTimeout)
	for {
		after, ok := i.bot.GetBlockNetworkID(target.Block.X(), target.Block.Y(), target.Block.Z())
		if ok && after != before {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		if !sleepContext(ctx, statePollInterval) {
			return false
		}
	}
}

// faceClickedPosition returns the click point on the face the bot is looking
// at, relative to the block's corner — the convention the protocol expects.
// Faces: 0 down, 1 up, 2 north (−Z), 3 south (+Z), 4 west (−X), 5 east (+X).
func faceClickedPosition(face int32) mgl32.Vec3 {
	switch face {
	case 0:
		return mgl32.Vec3{0.5, 0.0, 0.5}
	case 1:
		return mgl32.Vec3{0.5, 1.0, 0.5}
	case 2:
		return mgl32.Vec3{0.5, 0.5, 0.1}
	case 3:
		return mgl32.Vec3{0.5, 0.5, 0.9}
	case 4:
		return mgl32.Vec3{0.1, 0.5, 0.5}
	default: // 5, east
		return mgl32.Vec3{0.9, 0.5, 0.5}
	}
}

// heldItem returns the held stack and its hotbar index, both of which the
// server cross-checks against its own inventory view.
//
// StackNetworkID is deliberately left unset. It names a specific stack instance
// for ItemStackRequest tracking, NOT the item type — stuffing the item's type
// network ID in there reports a stack the server has never seen, which is the
// kind of silently-rejected interaction the placement skill warns about. The
// working placement code sends exactly {Stack: item} and nothing more.
func (i *Interactor) heldItem() (protocol.ItemInstance, int32) {
	slot := i.bot.GetHeldItemSlot()
	stack, ok := i.bot.GetInventorySlots()[slot]
	if !ok {
		return protocol.ItemInstance{}, int32(slot)
	}
	return protocol.ItemInstance{Stack: stack}, int32(slot)
}

// Resolve turns a request into a concrete target.
//
// A block reading is tried before an entity one because the words overlap ("a
// button" can be either), and where both exist the real block is the more
// common case. When a word named something and neither reading found anything,
// the request fails rather than falling through to "click whatever is in front"
// — that would click the wrong thing and then report success.
func (i *Interactor) Resolve(request Request) (Target, error) {
	botPos := i.bot.GetCoords()
	// Scanned once and shared: a named-block lookup, the raw-name fallback and
	// the "in front" fallback all need the same neighbourhood walk, and each one
	// is ~1000 cell queries.
	//
	// The visibility filter is applied here, once, rather than inside each
	// path's own filter. Named lookup used to be distance-only, so a door on
	// the far side of a wall was as clickable as the one in front — the one
	// action in this bot that could reach through geometry. Filtering the
	// candidates at the source is what keeps the named path and the in-front
	// path from disagreeing about what the bot can see.
	var blocks []Target
	blocksScanned := false
	scanBlocks := func() []Target {
		if !blocksScanned {
			blocks = onlyVisible(i.bot, botPos, scanBlockTargets(i.bot, botPos, searchRadius, request))
			blocksScanned = true
		}
		return blocks
	}

	if request.Block != "" {
		if target, ok := pickNearestBlock(scanBlocks(), func(b Target) bool {
			return BlockNameMatches(b.Name, request.Block)
		}, botPos, blockReach); ok {
			return target, nil
		}
	}
	if request.Entity != "" {
		if target, ok := i.resolveNamedEntity(request.Entity, botPos); ok {
			return target, nil
		}
	}
	if request.Block != "" || request.Entity != "" {
		// The vocabulary above covers vanilla words. A block from a behaviour
		// pack ("klik elevator_block") has no alias list, so before giving up,
		// match the words the player actually said against the block names that
		// are really standing there.
		if target, ok := pickNearestBlock(scanBlocks(), func(b Target) bool {
			return rawNameMatches(b.Name, request)
		}, botPos, blockReach); ok {
			return target, nil
		}
		return Target{}, notFoundError(request)
	}

	if target, ok := i.resolveInFront(botPos, scanBlocks()); ok {
		return target, nil
	}
	return Target{}, notFoundError(request)
}

// resolveNamedEntity matches a player by name first, then any entity by type.
func (i *Interactor) resolveNamedEntity(name string, botPos mgl32.Vec3) (Target, bool) {
	if id, pos, ok := i.bot.FindPlayer(name); ok && inReach(botPos, pos, entityReach) {
		return Target{Kind: KindEntity, ID: id, Name: name, Pos: aimPoint(pos)}, true
	}

	var best Target
	bestDist := float32(entityReach)
	found := false

	for id, ent := range i.bot.GetEntities() {
		if !isInteractableEntity(ent) {
			continue
		}
		if !EntityNameMatches(ent, name) {
			continue
		}
		dist := horizontalDistance(botPos, ent.Position)
		if dist < bestDist {
			bestDist = dist
			best = Target{Kind: KindEntity, ID: id, Name: ent.Type, Pos: aimPoint(ent.Position)}
			found = true
		}
	}
	return best, found
}

// ClickBlockAt clicks the block at an exact position. It is the entry point
// for higher-level actions (open chest, read sign) that already know which
// block they mean and must not be bitten by "in front" fallbacks: a named
// wrong target would click the wrong thing entirely.
func (i *Interactor) ClickBlockAt(ctx context.Context, pos protocol.BlockPos) (bool, string) {
	name, ok := i.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return false, "blok tidak termuat di cache dunia"
	}
	target := Target{
		Kind:  KindBlock,
		Name:  name,
		Block: pos,
		Pos:   blockAim(Target{Block: pos}),
		Face:  BlockFaceToward(pos, i.bot.GetCoords()),
	}
	return i.interactBlock(ctx, target)
}

// resolveInFront picks whatever the bot is facing: an entity first (server
// buttons and figures are entities far more often than blocks), then a block.
func (i *Interactor) resolveInFront(botPos mgl32.Vec3, blocks []Target) (Target, bool) {
	forward := ForwardVector(i.bot.GetYaw())

	var bestEntity Target
	entityDist := float32(entityReach)
	for id, ent := range i.bot.GetEntities() {
		if !isInteractableEntity(ent) {
			continue
		}
		if !withinCone(botPos, ent.Position, forward, frontConeDegrees) {
			continue
		}
		dist := horizontalDistance(botPos, ent.Position)
		if dist < entityDist {
			entityDist = dist
			bestEntity = Target{Kind: KindEntity, ID: id, Name: ent.Type, Pos: aimPoint(ent.Position)}
		}
	}
	if bestEntity.ID != 0 {
		return bestEntity, true
	}

	// blocks comes from the caller, which already scanned the neighbourhood for
	// any other part of this request.
	inFront := make([]Target, 0, len(blocks))
	for _, b := range blocks {
		if withinCone(botPos, blockAim(b), forward, frontConeDegrees) {
			inFront = append(inFront, b)
		}
	}
	return pickNearestBlock(inFront, nil, botPos, blockReach)
}

// pickNearestBlock returns the closest block in range that passes filter.
func pickNearestBlock(blocks []Target, filter func(Target) bool, from mgl32.Vec3, reach float32) (Target, bool) {
	best := Target{}
	bestDist := float32(reach)
	found := false
	for _, b := range blocks {
		if filter != nil && !filter(b) {
			continue
		}
		dist := horizontalDistance(from, blockAim(b))
		if dist < bestDist {
			bestDist = dist
			best = b
			found = true
		}
	}
	return best, found
}

// scanBlockTargets collects every loaded, clickable block around the bot.
// Clickable means a block that actually has an interaction: a door, a button, a
// chest. Plain stone and dirt are skipped so "click the thing in front" never
// lands on a wall — but a block that literally matches the words the player
// said is always kept, because behaviour-pack blocks ("elevator_block") have
// no interaction vocabulary entry of their own.
func scanBlockTargets(bot Bot, botPos mgl32.Vec3, radius int32, request Request) []Target {
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	targets := make([]Target, 0, 16)
	for dx := -radius; dx <= radius; dx++ {
		for dy := int32(-2); dy <= 3; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				pos := protocol.BlockPos{bx + dx, by + dy, bz + dz}
				name, ok := bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
				if !ok {
					continue
				}
				if !IsInteractiveBlockName(name) && !rawNameMatches(name, request) {
					continue
				}
				targets = append(targets, Target{
					Kind:  KindBlock,
					Name:  name,
					Block: pos,
					Pos:   blockAim(Target{Block: pos}),
					Face:  BlockFaceToward(pos, botPos),
				})
			}
		}
	}
	return targets
}

func blockAim(t Target) mgl32.Vec3 {
	return mgl32.Vec3{float32(t.Block.X()) + 0.5, float32(t.Block.Y()) + 0.5, float32(t.Block.Z()) + 0.5}
}

func blockKey(p protocol.BlockPos) string {
	return strconv.Itoa(int(p.X())) + "," + strconv.Itoa(int(p.Y())) + "," + strconv.Itoa(int(p.Z()))
}

func notFoundError(request Request) error {
	switch {
	case request.Entity != "":
		return fmt.Errorf("nggak ada '%s' yang bisa diklik di dekat sini", request.Entity)
	case request.Block != "":
		return fmt.Errorf("nggak ada '%s' di dekat sini", request.Block)
	default:
		return fmt.Errorf("nggak ada apa-apa yang bisa diklik di depan")
	}
}

func kindName(k Kind) string {
	if k == KindBlock {
		return "block"
	}
	return "entity"
}

func describeRequest(request Request) string {
	switch {
	case request.Entity != "":
		return request.Entity
	case request.Block != "":
		return request.Block
	default:
		return "in front"
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// normalise lowercases, strips the namespace, so "minecraft:oak_sign",
// "custom:oak_sign", "Oak Sign" and "oak sign" all compare equal.
func normalise(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if idx := strings.IndexByte(name, ':'); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

// rawNameMatches reports whether a nearby block's name is literally what the
// request asked for. Custom blocks from behaviour packs have no alias list, so
// a substring match on the player's own words is the only way to name them.
func rawNameMatches(blockName string, request Request) bool {
	raw := strings.TrimSpace(normalise(request.Raw))
	if raw == "" {
		return false
	}
	return strings.Contains(normalise(blockName), raw)
}
