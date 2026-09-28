// Package storage gives the bot a believable relationship with storage: it
// notices chests it can actually see, walks over, looks at the chest it means
// before opening it, and moves items through server-authoritative requests.
//
// Two rules make the difference between "a player using a chest" and "a bot
// cheating", and this package exists to enforce both:
//
//  1. A chest is only ever opened from where a player could open it — inside
//     reach, with a clear line of sight, after turning to face it. Clicking a
//     chest through a wall, or without aiming, is the single most obvious tell
//     that something is not a person.
//  2. Nothing moves optimistically. Every transfer goes out as an
//     ItemStackRequest and is applied from the server's authoritative
//     response, so the bot's view of its inventory is never a guess.
package storage

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// MaxStackSize mirrors the vanilla stack limit.
const MaxStackSize = 64

const (
	// openReach is how close the bot must stand to open a chest. Vanilla
	// interaction reach is about 4.5 blocks, but a player walks right up to
	// storage; standing at the reach limit reads as a bot working to a
	// distance budget rather than as a person opening a chest.
	openReach = 2.75

	// searchRadius is how far around the bot containers are looked for.
	searchRadius = 12

	// containerSlots is the size of a single chest's window. A double chest is
	// 27 per half; reading past 27 would index into a region whose slot
	// numbering depends on the host, and would invent items.
	containerSlots = 27

	// eyeHeight is the camera offset above the feet, matching the eye position
	// the click transaction carries.
	eyeHeight = 1.62

	// losStep is the sampling step of the line-of-sight walk.
	losStep = 0.25
)

// Bot is the slice of the bot the search logic needs. Keeping it narrow is
// what makes discovery testable without a live connection.
type Bot interface {
	GetCoords() mgl32.Vec3
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	GetBlockName(x, y, z int32) (string, bool)
	// BlockLoaded reports whether the bot has terrain knowledge for a cell,
	// and whether that cell is solid. Sight is checked against known terrain
	// only: an unknown cell cannot occlude, because the bot has no idea what
	// is in it, and inventing a wall is worse than the small risk of aiming
	// through an unknown gap.
	BlockLoaded(x, y, z int32) (solid bool, loaded bool)
	// SignText returns the text of a sign at a position, when the chunk
	// payload carried it. Sign text lives in block entities, so this is empty
	// until the chunk holding the sign has been decoded.
	SignText(x, y, z int32) (string, bool)
	// InFieldOfView reports whether a world point is inside the bot's vision
	// cone.
	//
	// It is a method rather than a direct perception call because this package
	// cannot import perception: the bot imports storage, so perception->storage
	// would close a cycle. The bot wires it to the same cone the block summary
	// uses, which is the point — "a chest the bot can see" has to mean the same
	// thing here as everywhere else.
	InFieldOfView(point mgl32.Vec3) bool
}

// Container is the read/write session for one open chest window. The concrete
// implementation lives on the bot, because it needs the container session
// state and the stack-request machinery.
type Container interface {
	// WindowID is the window the server assigned when the chest was opened.
	WindowID() byte
	// Items returns the latest known contents, keyed by slot.
	Items() map[uint32]protocol.ItemInstance
	// ItemName resolves a stack to a display name.
	ItemName(item protocol.ItemInstance) string
	// Take moves up to count items out of the container into the inventory.
	Take(ctx context.Context, slot uint32, count int, stackNetID int32, itemName string) error
	// Store moves up to count items from an inventory slot into a container
	// slot, merging with what is already there.
	Store(ctx context.Context, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error
	// FindInventorySlotFor reports where an item can land in the bot's
	// inventory, and whether there is room at all.
	FindInventorySlotFor(name string) (uint32, bool)
	// FindInventoryItem locates an item in the bot's inventory.
	FindInventoryItem(name string) (slot uint32, itemName string, count int, ok bool)
	// Close ends the window the way a player does.
	Close()
}

// Chest is one container block the bot can see.
type Chest struct {
	Pos      protocol.BlockPos
	Name     string
	Distance float32
	// Label is the text of a nearby sign that plausibly points at this chest.
	// It is what lets a *labelled* storage room be searched in one deliberate
	// pass instead of opening every chest in the dark.
	Label string
	// HasLineOf is true when nothing known blocks the walk from the bot's eyes
	// to the chest. A chest behind a wall is not a candidate at all.
	HasLineOf bool
	// Reachable is true when the bot can already stand in open reach.
	Reachable bool
}

// Service finds and approaches containers. It holds no world state of its own,
// so a rejoin cannot leave it reasoning about a world that is gone.
type Service struct {
	bot Bot
}

// New builds a Service.
func New(bot Bot) *Service {
	return &Service{bot: bot}
}

// ChestAt returns the description of one container block, with its distance and
// line of sight resolved against the bot's current position. ok is false when
// the block is not visible from here, which is the whole point: a chest the
// bot cannot see is not a chest it may open.
func (s *Service) ChestAt(pos protocol.BlockPos, name string) (Chest, bool) {
	center := blockCenter(pos)
	origin := s.bot.GetCoords()
	dist := center.Sub(origin).Len()
	if !s.visibleFrom(origin, center, pos) {
		return Chest{}, false
	}
	return Chest{
		Pos:       pos,
		Name:      name,
		Distance:  dist,
		HasLineOf: true,
		Reachable: dist <= openReach+1.5,
	}, true
}

// IsContainerBlock reports whether a block name is a storage container.
//
// The explicit list covers vanilla blocks. The suffix fallback is for
// server-specific storage ("forestry:common_storage", a modded vault), which
// no fixed list could keep up with; matching the suffix stops the bot walking
// past a storage block because it was renamed.
func IsContainerBlock(name string) bool {
	switch normalise(name) {
	case "chest", "trapped_chest", "ender_chest", "barrel", "hopper",
		"undyed_shulker_box", "undyed_hopper", "wooden_hopper", "stone_hopper",
		"white_shulker_box", "orange_shulker_box", "magenta_shulker_box",
		"light_blue_shulker_box", "yellow_shulker_box", "lime_shulker_box",
		"pink_shulker_box", "gray_shulker_box", "light_gray_shulker_box",
		"cyan_shulker_box", "purple_shulker_box", "blue_shulker_box",
		"brown_shulker_box", "green_shulker_box", "red_shulker_box",
		"black_shulker_box":
		return true
	}
	trimmed := normalise(name)
	return strings.HasSuffix(trimmed, "chest") || strings.HasSuffix(trimmed, "shulker_box") ||
		strings.HasSuffix(trimmed, "barrel") || strings.HasSuffix(trimmed, "_storage")
}

// FindContainers returns the visible containers around the bot, nearest first,
// with line of sight and reach already resolved.
func (s *Service) FindContainers() []Chest {
	origin := s.bot.GetCoords()
	bx := int32(math.Floor(float64(origin.X())))
	by := int32(math.Floor(float64(origin.Y())))
	bz := int32(math.Floor(float64(origin.Z())))

	out := make([]Chest, 0, 8)
	for dx := int32(-searchRadius); dx <= searchRadius; dx++ {
		for dy := int32(-3); dy <= 4; dy++ {
			for dz := int32(-searchRadius); dz <= searchRadius; dz++ {
				pos := protocol.BlockPos{bx + dx, by + dy, bz + dz}
				name, ok := s.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
				if !ok || !IsContainerBlock(name) {
					continue
				}
				center := blockCenter(pos)
				dist := center.Sub(origin).Len()
				if dist > float32(searchRadius)+2 {
					continue
				}
				if !s.visibleFrom(origin, center, pos) {
					continue
				}
				out = append(out, Chest{
					Pos:       pos,
					Name:      name,
					Distance:  dist,
					HasLineOf: true,
					Reachable: dist <= openReach+1.5,
				})
			}
		}
	}
	// Nearest first, with a stable positional tiebreak so two consecutive
	// searches agree on the order. Unordered iteration would otherwise make
	// the bot hop between two equally-near chests, which looks like a decision
	// loop rather than a plan.
	sort.SliceStable(out, func(i, j int) bool {
		if math.Abs(float64(out[i].Distance-out[j].Distance)) < 0.01 {
			return blockKey(out[i].Pos) < blockKey(out[j].Pos)
		}
		return out[i].Distance < out[j].Distance
	})
	return out
}

// lineOfSight walks from the bot's eyes to the container centre. The target
// cell itself is never an occluder, and unknown cells never block.
func (s *Service) lineOfSight(origin, target mgl32.Vec3, targetPos protocol.BlockPos) bool {
	return s.lineOfSightBetween(origin, target, targetPos, targetPos)
}

// visibleFrom is the full visibility test for something the bot is deciding
// whether to act on: inside the vision cone, and not behind anything solid.
//
// The cone is checked first and is cheap, so a container the bot is facing away
// from never costs a ray walk. This is the rule that makes "the chest over
// there" mean a chest the bot could actually point at, rather than any chest
// inside a radius — the difference between looking for storage and seeing
// through walls.
func (s *Service) visibleFrom(origin, target mgl32.Vec3, targetPos protocol.BlockPos) bool {
	if !s.bot.InFieldOfView(target) {
		return false
	}
	return s.lineOfSight(origin, target, targetPos)
}

// lineOfSightBetween is lineOfSight with a second endpoint to exclude. Both
// endpoints are excluded because a label test always has two solid endpoints
// (the sign and the chest) and neither should occlude the other.
//
// This is pure line of sight on purpose. The vision cone is applied by
// visibleFrom, which is the only place that knows the geometry is measured from
// the bot; applying it here would test a sign against the chest's field of view
// rather than the bot's.
func (s *Service) lineOfSightBetween(origin, target mgl32.Vec3, endpointA, endpointB protocol.BlockPos) bool {
	eye := origin.Add(mgl32.Vec3{0, eyeHeight, 0})
	delta := target.Sub(eye)
	length := delta.Len()
	if length < 0.001 {
		return true
	}
	steps := int(length/losStep) + 1
	for i := 1; i < steps; i++ {
		p := eye.Add(delta.Mul(float32(i) / float32(steps)))
		cell := protocol.BlockPos{
			int32(math.Floor(float64(p.X()))),
			int32(math.Floor(float64(p.Y()))),
			int32(math.Floor(float64(p.Z()))),
		}
		if cell == endpointA || cell == endpointB {
			continue
		}
		if solid, loaded := s.bot.BlockLoaded(cell.X(), cell.Y(), cell.Z()); loaded && solid {
			return false
		}
	}
	return true
}

// Approach walks to a stand-off point beside the container and waits until the
// bot is genuinely within reach.
//
// It reports failure honestly: a chest across a gap or up a wall cannot be
// opened from here, and claiming otherwise would have the bot tell a player an
// item was stored when nothing happened.
func (s *Service) Approach(ctx context.Context, chest Chest) error {
	center := blockCenter(chest.Pos)
	if s.distanceTo(center) <= openReach {
		s.bot.StopMovement()
		return nil
	}

	stand := s.standCell(center)
	s.bot.NavigateToBlock(stand[0], stand[1], stand[2], 1.0)

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.distanceTo(center) <= openReach+0.5 {
			s.bot.StopMovement()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.bot.StopMovement()
	if s.distanceTo(center) <= openReach+1.0 {
		return nil
	}
	return fmt.Errorf("tidak bisa mendekati chest di %s", blockKey(chest.Pos))
}

func (s *Service) distanceTo(center mgl32.Vec3) float32 {
	return center.Sub(s.bot.GetCoords()).Len()
}

// standCell picks the open cell next to the container, preferring the side the
// bot is already on.
//
// The neighbour order is fixed rather than random, which is what makes the
// approach repeatable — and a repeatable approach is what reads as deliberate.
// It also avoids standing inside a neighbouring chest, which a storage room
// full of chests makes very easy to do by accident.
func (s *Service) standCell(center mgl32.Vec3) [3]int32 {
	origin := s.bot.GetCoords()
	base := protocol.BlockPos{
		int32(math.Floor(float64(center.X()))),
		int32(math.Floor(float64(center.Y()))),
		int32(math.Floor(float64(center.Z()))),
	}
	dirX := sign32(base.X() - int32(math.Floor(float64(origin.X()))))
	dirZ := sign32(base.Z() - int32(math.Floor(float64(origin.Z()))))
	if dirX == 0 && dirZ == 0 {
		dirZ = 1
	}

	candidates := [][2]int32{
		{dirX, dirZ},
		{dirX, 0},
		{0, dirZ},
		{dirZ, dirX},
		{dirX, -dirZ},
		{-dirX, dirZ},
		{0, -dirZ},
		{-dirX, 0},
		{-dirX, -dirZ},
	}
	for _, c := range candidates {
		cell := [3]int32{base.X() + c[0], base.Y(), base.Z() + c[1]}
		if s.cellOpen(cell) {
			return cell
		}
	}
	// Nothing open beside it: stay put rather than walk into geometry.
	return [3]int32{
		int32(math.Floor(float64(origin.X()))),
		int32(math.Floor(float64(origin.Y()))),
		int32(math.Floor(float64(origin.Z()))),
	}
}

func (s *Service) cellOpen(cell [3]int32) bool {
	feetSolid, feetLoaded := s.bot.BlockLoaded(cell[0], cell[1]-1, cell[2])
	if feetLoaded && !feetSolid {
		return false
	}
	for dy := int32(0); dy < 2; dy++ {
		if solid, loaded := s.bot.BlockLoaded(cell[0], cell[1]+dy, cell[2]); loaded && solid {
			return false
		}
	}
	return true
}

// Open approaches the chest, turns to face it, waits for the aim to settle,
// then asks the caller to click it and hand back the open session.
//
// The callback is how the click itself stays in the interactor package, which
// already owns the proven packet shapes (aim convergence, arm swing, the wire
// block runtime ID, the inline retry transport). Re-deriving them here would
// mean a chest opens on a different set of servers than a door does.
func (s *Service) Open(ctx context.Context, chest Chest, open func(ctx context.Context, pos protocol.BlockPos) (Container, error)) (Container, error) {
	if !chest.HasLineOf {
		return nil, fmt.Errorf("chest di %s tidak kelihatan dari sini", blockKey(chest.Pos))
	}
	if err := s.Approach(ctx, chest); err != nil {
		return nil, err
	}
	// Turn to face the chest before the click. A player looks at the thing it
	// is opening, and the click is sent from the aim the server last received.
	s.bot.LookAt(chestAimPoint(blockCenter(chest.Pos)))
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(120 * time.Millisecond):
	}
	return open(ctx, chest.Pos)
}

// Item is one stack read out of an open container.
type Item struct {
	Slot  uint32
	Name  string
	Count int
}

// ReadItems turns a container's slot map into a sorted, displayable list.
func ReadItems(c Container) []Item {
	slots := c.Items()
	out := make([]Item, 0, len(slots))
	for slot, instance := range slots {
		if instance.Stack.Count <= 0 || slot >= containerSlots {
			continue
		}
		out = append(out, Item{
			Slot:  slot,
			Name:  c.ItemName(instance),
			Count: int(instance.Stack.Count),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// FindInContainer locates a stack in an open container by name.
func FindInContainer(c Container, itemName string) (uint32, protocol.ItemInstance, bool) {
	slots := c.Items()
	want := strings.ToLower(itemName)
	for slot := uint32(0); slot < containerSlots; slot++ {
		instance, ok := slots[slot]
		if !ok || instance.Stack.Count <= 0 {
			continue
		}
		if strings.Contains(strings.ToLower(c.ItemName(instance)), want) {
			return slot, instance, true
		}
	}
	return 0, protocol.ItemInstance{}, false
}

// DestinationFor picks the container slot an item should be stored into: the
// first stack of the same item that still has room, otherwise the first empty
// slot. It returns that slot and the authoritative stack ID already there (0
// when empty), because the server cross-checks the value.
func DestinationFor(c Container, itemName string) (slot uint32, stackNetID int32, ok bool) {
	slots := c.Items()
	want := strings.ToLower(itemName)
	for i := uint32(0); i < containerSlots; i++ {
		instance, exists := slots[i]
		if !exists || instance.Stack.Count <= 0 {
			continue
		}
		if int(instance.Stack.Count) < MaxStackSize && strings.Contains(strings.ToLower(c.ItemName(instance)), want) {
			return i, instance.StackNetworkID, true
		}
	}
	for i := uint32(0); i < containerSlots; i++ {
		if instance, exists := slots[i]; !exists || instance.Stack.Count <= 0 {
			return i, 0, true
		}
	}
	return 0, 0, false
}

// TakeItem moves up to count of itemName out of an open container into the
// bot's inventory, and returns how many actually moved.
//
// The count is what the caller reports to a human. A partial take must read as
// partial: a bot that claims six logs and delivers two is worse than one that
// admits it only found two.
func (s *Service) TakeItem(ctx context.Context, c Container, itemName string, count int) (int, error) {
	slot, instance, ok := FindInContainer(c, itemName)
	if !ok {
		return 0, fmt.Errorf("tidak ada %s di chest itu", itemName)
	}
	if _, hasRoom := c.FindInventorySlotFor(c.ItemName(instance)); !hasRoom {
		return 0, fmt.Errorf("tas penuh, tidak bisa mengambil %s", itemName)
	}
	want := count
	if want <= 0 || want > int(instance.Stack.Count) {
		want = int(instance.Stack.Count)
	}
	if err := c.Take(ctx, slot, want, instance.StackNetworkID, c.ItemName(instance)); err != nil {
		return 0, err
	}
	return want, nil
}

// StoreItem moves up to count of itemName from the bot's inventory into an
// open container.
func (s *Service) StoreItem(ctx context.Context, c Container, itemName string, count int) (int, error) {
	srcSlot, srcName, _, ok := c.FindInventoryItem(itemName)
	if !ok {
		return 0, fmt.Errorf("tidak ada %s di tas", itemName)
	}
	dstSlot, dstNetID, ok := DestinationFor(c, srcName)
	if !ok {
		return 0, fmt.Errorf("chest penuh, tidak bisa menyimpan %s", srcName)
	}
	want := count
	if want <= 0 {
		want = MaxStackSize
	}
	if err := c.Store(ctx, dstSlot, dstNetID, srcSlot, want); err != nil {
		return 0, err
	}
	return want, nil
}

// DescribeItems renders a container's contents as a short human sentence, the
// form both the chat reply and the Jev state text use.
func DescribeItems(c Container) string {
	items := ReadItems(c)
	if len(items) == 0 {
		return "kosong"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%s x%d", item.Name, item.Count))
	}
	return strings.Join(parts, ", ")
}

func sign32(v int32) int32 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	default:
		return 0
	}
}

func blockCenter(p protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{float32(p.X()) + 0.5, float32(p.Y()) + 0.5, float32(p.Z()) + 0.5}
}

// chestAimPoint aims at the lid rather than the middle of the block, which is
// where a player looks when opening storage.
func chestAimPoint(center mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{center.X(), center.Y() + 0.25, center.Z()}
}

func blockKey(p protocol.BlockPos) string {
	return fmt.Sprintf("%d,%d,%d", p.X(), p.Y(), p.Z())
}

func normalise(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if idx := strings.Index(name, ":"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}
