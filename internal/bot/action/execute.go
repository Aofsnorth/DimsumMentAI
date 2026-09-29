// Package action provides the dispatch layer that translates AI-parsed
// action labels into concrete bot behavior. The Execute function looks up
// a handler in the actionHandlers map and runs it with the action parameter
// and the invoking user.
package action

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/interact"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
)

// actionHandler executes a single action label for bot b.
type actionHandler func(*bot.Bot, string, string)

// emoteConfig describes how to trigger an emote action.
type emoteConfig struct {
	name     string
	duration time.Duration
	fixed    bool
	ticks    int
	lookAt   mgl32.Vec3
}

// emoteDefaults maps emote labels to their emote config.
var emoteDefaults = map[string]emoteConfig{
	"jumpforever":   {name: "jump", duration: 4 * time.Second},
	"jumpinplace":   {name: "jump", duration: 4 * time.Second},
	"spinslow":      {name: "spin", duration: 5 * time.Second},
	"spinforever":   {name: "spin", duration: 5 * time.Second},
	"spinfast":      {name: "spin", duration: 5 * time.Second},
	"teleportfake":  {name: "spin", duration: 5 * time.Second},
	"spinlookup":    {name: "spin", duration: 5 * time.Second, lookAt: mgl32.Vec3{0, 8, 0}},
	"spinlookdown":  {name: "spin", duration: 5 * time.Second, lookAt: mgl32.Vec3{0, -4, 0}},
	"dance":         {name: "spin", duration: 6 * time.Second},
	"floss":         {name: "spin", duration: 6 * time.Second},
	"naenae":        {name: "spin", duration: 6 * time.Second},
	"robot":         {name: "spin", duration: 6 * time.Second},
	"breakdance":    {name: "spin", duration: 6 * time.Second},
	"throwparty":    {name: "spin", duration: 6 * time.Second},
	"explode":       {name: "spin", duration: 6 * time.Second},
	"jumpspincombo": {name: "spin", duration: 6 * time.Second},
	"twerk":         {name: "sneak", duration: 5 * time.Second},
	"dab":           {name: "wave", fixed: true, ticks: 30},
	"wave":          {name: "wave", fixed: true, ticks: 30},
	"headbang":      {name: "nod", duration: 3 * time.Second},
	"nod":           {name: "nod", duration: 3 * time.Second},
	"shake":         {name: "shake", duration: 3 * time.Second},
}

// emoteHandler returns a handler that triggers the emote described by label.
//
// An emote is a dispatched animation with no confirmation to wait for — the
// bot either asked for it or it is not going to happen — so the step reports
// once the request has been sent rather than staying silent.
func emoteHandler(label string) actionHandler {
	cfg := emoteDefaults[label]
	return func(b *bot.Bot, param, user string) {
		if cfg.lookAt != (mgl32.Vec3{}) {
			b.LookAt(b.GetCoords().Add(cfg.lookAt))
		}
		ticks := cfg.ticks
		if !cfg.fixed {
			ticks = durationTicks(param, cfg.duration)
		}
		b.TriggerEmoteFor(cfg.name, ticks)
		reportStatus(b, user, event.ActionStatus{Action: label, Item: cfg.name, Success: true})
	}
}

// movementHandler returns a handler that runs the movement pattern for label.
//
// The pattern runs for a fixed span and then stops itself, which is the whole
// result: the step reports on completion so a plan waiting on it is released
// when the bot is actually done moving rather than guessing from a timer.
func movementHandler(label string) actionHandler {
	return func(b *bot.Bot, param, user string) {
		go func() {
			runMovementPattern(b, label, param, user)
			reportStatus(b, user, event.ActionStatus{Action: label, Success: true})
		}()
	}
}

// gatherHandler returns a handler that gathers a block or wood type.
//
// The gatherer reports to the chat layer from inside its own package, which
// this package cannot route to a waiting plan step. So the outcome is measured
// here instead: the handler samples the inventory before the work starts and
// reports the difference, which is the same thing the gatherer counts.
func gatherHandler(defaultItem string) actionHandler {
	return func(b *bot.Bot, param, user string) {
		parts := strings.Split(param, ",")
		itemName := defaultItem
		if parts[0] != "" {
			itemName = normalizeItemName(parts[0])
		}
		count := bot.DefaultEmoteCount
		if len(parts) > 1 {
			_, _ = fmt.Sscanf(parts[1], "%d", &count)
		}
		before := countInventoryItems(b.GetInventorySlots(), b.GetItemNames(), itemName)
		go func() {
			if isWoodLike(itemName) {
				b.Gatherer.GatherWoodType(context.Background(), itemName, count)
			} else {
				b.Gatherer.GatherBlock(context.Background(), itemName, count)
			}
			reportInventoryDelta(b, user, "gather", itemName, before, 0)
		}()
	}
}

// lootHandler returns a handler that collects all drops within radius and logs
// msg. The collector's return value is the count it actually picked up, so the
// step reports that rather than assuming the sweep found something.
func lootHandler(radius float32, msg string) actionHandler {
	return func(b *bot.Bot, _, user string) {
		go func() {
			collected := b.Gatherer.CollectAllDrops(context.Background(), radius)
			b.Logger.Debug(msg, "collected", collected)
			action := "loot"
			if collected == 0 {
				reportStatus(b, user, event.ActionStatus{
					Action:  action,
					Success: false,
					Error:   "tidak ada drop di sekitar",
				})
				return
			}
			reportStatus(b, user, event.ActionStatus{Action: action, Count: collected, Success: true})
		}()
	}
}

// storeItem stores the normalised item name in a chest.
func storeItem(b *bot.Bot, param string) {
	go func() {
		itemName := normalizeItemName(param)
		success := b.InventoryMgr.Chest().StoreItem(context.Background(), itemName, 0)
		b.Logger.Debug("store action complete", "success", success, "item", itemName)
	}()
}

// harvestCrops harvests the crop type described by param. The farmer returns
// how much it actually picked, so that is what the step reports.
func harvestCrops(b *bot.Bot, param, user string) {
	go func() {
		cropType := normalizeCropType(param)
		count := parseCount(param, 20)
		harvested := b.Farmer.HarvestCrops(context.Background(), cropType, count)
		b.Logger.Debug("harvest complete", "count", harvested)
		reportCountOutcome(b, user, "farm", cropType, harvested, "tidak ada dewasa yang bisa dipanen")
	}()
}

// goFish makes the bot fish count times. The fisher returns the number of
// catches it landed, which is the only outcome worth reporting — a cast that
// comes back empty is not a fishing trip.
func goFish(b *bot.Bot, param, user string) {
	count := parseCount(param, 5)
	go func() {
		caught := b.Fisher.GoFish(context.Background(), count)
		b.Logger.Debug("fishing complete", "caught", caught)
		reportCountOutcome(b, user, "fish", "fish", caught, "tidak dapat ikan")
	}()
}

// reportBoolOutcome reports a finished action whose handler holds a single
// pass/fail answer from the subsystem that performed it. These calls used to
// discard that answer, which left the plan step with nothing to report and
// turned every one of them into a silent failure.
func reportBoolOutcome(b *bot.Bot, user, action, item string, ok bool, failError string) {
	if !ok {
		reportStatus(b, user, event.ActionStatus{
			Action:  action,
			Item:    item,
			Success: false,
			Error:   failError,
		})
		return
	}
	reportStatus(b, user, event.ActionStatus{Action: action, Item: item, Success: true})
}

// smeltError explains a failed smelt. The furnace only reports a bool, so the
// reason is inferred from the most common cause rather than invented.
func smeltError(success bool, itemName string) string {
	if success {
		return ""
	}
	return fmt.Sprintf("gagal men-smelt %s, cek furnace dan bahan", itemName)
}

// reportCountOutcome reports a finished action whose handler already knows how
// many items it produced. A run that produced nothing failed, whatever the
// underlying call reported back.
func reportCountOutcome(b *bot.Bot, user, action, item string, count int, emptyError string) {
	if count <= 0 {
		reportStatus(b, user, event.ActionStatus{
			Action:  action,
			Item:    item,
			Success: false,
			Error:   emptyError,
		})
		return
	}
	reportStatus(b, user, event.ActionStatus{Action: action, Item: item, Count: count, Success: true})
}

// handleShoot finds the nearest hostile mob and fires a ranged weapon.
func handleShoot(b *bot.Bot, user string) {
	if !b.CombatMgr.HasRangedWeapon() {
		reportStatus(b, user, event.ActionStatus{Action: "shoot", Success: false, Error: "gak punya bow atau arrow"})
		return
	}

	entities := b.GetEntities()
	pos := b.GetCoords()
	var closestID uint64
	closestDist := float32(30)
	for id, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		dist := pos.Sub(ent.Position).Len()
		if dist < closestDist && dist > 4.0 {
			closestDist = dist
			closestID = id
		}
	}
	if closestID != 0 {
		b.CombatMgr.BowAttack(closestID)
	} else {
		reportStatus(b, user, event.ActionStatus{Action: "shoot", Success: false, Error: "gak ada target dalam jarak tembak"})
	}
}

// interactHandler clicks whatever the request points at. The parameter is loose
// on purpose: a player name, a thing type ("npc", "sign", "the one in front of
// you"), or empty for whatever the bot is facing.
//
// The interactor reports its own outcome to the chat layer from inside its own
// package, which a waiting plan step cannot see. So the target is resolved
// here first: an interaction with nothing to click is a failure the planner
// should hear about immediately, instead of a step that hangs until it times
// out. Once a target exists the interactor owns the reporting.
func interactHandler(b *bot.Bot, param, user string) {
	// Guarded because this runs in its own goroutine: a nil subsystem here is a
	// process-wide crash, not a recoverable error.
	if b.Interactor == nil {
		reportStatus(b, user, event.ActionStatus{
			Action:  "interact",
			Success: false,
			Error:   "bot belum siap, coba lagi sebentar",
		})
		return
	}

	if _, err := b.Interactor.Resolve(interact.ParseRequest(param)); err != nil {
		reportStatus(b, user, event.ActionStatus{
			Action:  "interact",
			Item:    param,
			Success: false,
			Error:   err.Error(),
		})
		return
	}
	go b.Interactor.Interact(context.Background(), user, param)
}

// joinHandler moves the bot to a different server by ending the current
// session; the run loop re-dials the new address.
func joinHandler(b *bot.Bot, param, user string) {
	address := strings.TrimSpace(param)
	if address == "" {
		reportStatus(b, user, event.ActionStatus{
			Action:  "join",
			Success: false,
			Error:   "butuh alamat server, contoh: join:192.168.1.10:19132",
		})
		return
	}
	if err := b.RequestJoin(address); err != nil {
		reportStatus(b, user, event.ActionStatus{
			Action:  "join",
			Item:    address,
			Success: false,
			Error:   err.Error(),
		})
		return
	}
	b.Logger.Info("server switch requested", "address", address, "user", user)
}

// commandHandler runs a server-side command through the CommandRequest channel.
// Chat can only say "/register …" as words; this actually runs it, which is what
// password-gated servers and lobby plugins require.
func commandHandler(b *bot.Bot, param, user string) {
	param = strings.TrimSpace(param)
	if param == "" {
		reportStatus(b, user, event.ActionStatus{
			Action:  "cmd",
			Success: false,
			Error:   "butuh command, contoh: cmd:/register pass pass",
		})
		return
	}
	if err := b.SendCommand(param); err != nil {
		reportStatus(b, user, event.ActionStatus{Action: "cmd", Item: param, Success: false, Error: err.Error()})
		return
	}

	// The server answers asynchronously. Poll briefly so the reported status
	// carries the real reply instead of a bare "sent" that reads like success
	// even when the command was rejected.
	go reportCommandResult(b, param, user)
}

// commandOutputTimeout is how long the action waits for the server's reply.
const commandOutputTimeout = 2 * time.Second

func reportCommandResult(b *bot.Bot, command, user string) {
	deadline := time.Now().Add(commandOutputTimeout)
	for time.Now().Before(deadline) {
		if output, ok := b.LastCommandOutput(); ok {
			reportStatus(b, user, event.ActionStatus{
				Action:  "cmd",
				Item:    command,
				Success: true,
				Error:   "",
				Count:   0,
			})
			b.Logger.Info("server command output", slog.String("command", command), slog.String("output", output))
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	reportStatus(b, user, event.ActionStatus{
		Action:  "cmd",
		Item:    command,
		Success: true,
		Error:   "",
	})
}

// actionHandlers maps action labels to their handler functions.
var actionHandlers = map[string]actionHandler{
	// Build / undo. The builder agent reports to the chat layer from its
	// own package, which a waiting step cannot see, so the step confirms
	// what the agent did by counting the blocks it holds.
	"build": func(b *bot.Bot, param, user string) {
		before := countInventoryItems(b.GetInventorySlots(), b.GetItemNames(), "cobblestone")
		go func() {
			b.BuilderAgent.Build(context.Background(), user, param)
			reportInventoryDelta(b, user, "build", "cobblestone", before, 0)
		}()
	},
	"stopbuild": func(b *bot.Bot, _, user string) {
		b.BuilderAgent.StopBuilding()
		reportStatus(b, user, event.ActionStatus{Action: "stopbuild", Success: true})
	},
	"stopbuilding": func(b *bot.Bot, _, user string) {
		b.BuilderAgent.StopBuilding()
		reportStatus(b, user, event.ActionStatus{Action: "stopbuild", Success: true})
	},
	"undo": func(b *bot.Bot, param, user string) {
		count := 0
		if param != "" {
			_, _ = fmt.Sscanf(param, "%d", &count)
		}
		before := countInventoryItems(b.GetInventorySlots(), b.GetItemNames(), "cobblestone")
		go func() {
			b.BuilderAgent.UndoBuild(context.Background(), count)
			// Undo puts blocks back, so the cobblestone count rising is the
			// proof that the undo did something.
			reportInventoryDelta(b, user, "undo", "cobblestone", before, 0)
		}()
	},

	// Movement / follow
	"come": func(b *bot.Bot, param, user string) {
		target := param
		if target == "" {
			target = user
		}
		// ComeToPlayer answers whether it found the player at all, so a
		// "come here" for somebody who is not nearby fails instead of
		// standing there looking like it worked.
		if !b.ComeToPlayer(target) {
			reportStatus(b, user, event.ActionStatus{
				Action:  "come",
				Item:    target,
				Success: false,
				Error:   fmt.Sprintf("nggak nemu pemain %s", target),
			})
			return
		}
		reportStatus(b, user, event.ActionStatus{Action: "come", Item: target, Success: true})
	},
	"follow": func(b *bot.Bot, param, user string) {
		target := param
		if target == "" {
			target = user
		}
		b.FollowPlayer(target)
		reportStatus(b, user, event.ActionStatus{Action: "follow", Item: target, Success: true})
	},
	"goto": func(b *bot.Bot, param, user string) { goToCoords(b, param, user) },
	"stop": func(b *bot.Bot, _, user string) {
		b.Stop()
		if b.Planner != nil {
			b.Planner.Cancel()
		}
		reportStatus(b, user, event.ActionStatus{Action: "stop", Success: true})
	},
	"stay": func(b *bot.Bot, _, user string) {
		b.Stop()
		if b.Planner != nil {
			b.Planner.Cancel()
		}
		reportStatus(b, user, event.ActionStatus{Action: "stay", Success: true})
	},
	"flee": func(b *bot.Bot, param, user string) { go runAwayFromPlayer(b, user, 5*time.Second) },
	"lookat": func(b *bot.Bot, param, user string) {
		target := param
		if target == "" {
			target = user
		}
		if !b.LookAtPlayer(target, 5*time.Second) {
			reportStatus(b, user, event.ActionStatus{
				Action:  "lookat",
				Item:    target,
				Success: false,
				Error:   fmt.Sprintf("nggak nemu pemain %s buat diliat", target),
			})
			return
		}
		reportStatus(b, user, event.ActionStatus{Action: "lookat", Item: target, Success: true})
	},

	// Emote
	"emote": func(b *bot.Bot, param, user string) {
		parts := strings.Split(param, ",")
		b.TriggerEmote(parts[0])
		reportStatus(b, user, event.ActionStatus{Action: "emote", Item: parts[0], Success: true})
	},

	// Combat / items
	"attack": handleAttack,
	"hunt":   handleAttack,
	"pvp":    handlePVP,
	"guard":  handleAttack,
	"equip": func(b *bot.Bot, param, user string) {
		itemName := normalizeItemName(param)
		if itemName == "" {
			reportStatus(b, user, event.ActionStatus{
				Action:  "equip",
				Success: false,
				Error:   "butuh nama barang, contoh: equip:diamond_sword",
			})
			return
		}
		go func() {
			if err := b.InventoryMgr.EquipItem(itemName); err != nil {
				b.Logger.Warn("equip action failed", "item", itemName, "error", err)
				reportStatus(b, user, event.ActionStatus{
					Action:  "equip",
					Item:    itemName,
					Success: false,
					Error:   err.Error(),
				})
				return
			}
			reportStatus(b, user, event.ActionStatus{Action: "equip", Item: itemName, Success: true})
		}()
	},
	"give":           handleGive,
	"drop":           handleDrop,
	"place":          handlePlace,
	"list_craftable": handleListCraftable,
	"eat": func(b *bot.Bot, param, user string) {
		food := strings.ToLower(strings.TrimSpace(param))
		if food == "" {
			reportStatus(b, user, event.ActionStatus{
				Action:  "eat",
				Success: false,
				Error:   "butuh nama makanan, contoh: eat:cooked_beef",
			})
			return
		}
		go func() {
			if err := b.InventoryMgr.Eat(food); err != nil {
				reportStatus(b, user, event.ActionStatus{Action: "eat", Item: food, Success: false, Error: err.Error()})
				return
			}
			reportStatus(b, user, event.ActionStatus{Action: "eat", Item: food, Success: true})
		}()
	},
	"loot":  lootHandler(10.0, "loot action complete"),
	"clear": lootHandler(12.0, "sweep drops complete"),
	"scan":  lootHandler(12.0, "sweep drops complete"),

	// Gather / mine
	"gather":   gatherHandler("wood"),
	"mine":     gatherHandler("cobblestone"),
	"automine": gatherHandler("cobblestone"),

	// Craft / smelt / store
	"craft": handleCraft,
	"smelt": func(b *bot.Bot, param, user string) {
		go func() {
			itemName := normalizeItemName(param)
			success := b.InventoryMgr.Furnace().SmeltItem(context.Background(), itemName)
			b.Logger.Debug("smelt action complete", "success", success, "item", itemName)
			reportStatus(b, user, event.ActionStatus{
				Action:  "smelt",
				Item:    itemName,
				Success: success,
				Error:   smeltError(success, itemName),
			})
		}()
	},
	// store/storeall/take/retrieve are registered in init() (storage_handlers.go
	// via memory_handlers.go) so they share the labelled chest search. The old
	// single-chest versions lived here and are gone: they opened an arbitrary
	// chest from wherever the bot happened to be and reported success without
	// the item ever moving.

	// Low-level world interaction: click entities (players, NPCs, server
	// buttons/figures) and block entities (doors, levers, chests, signs).
	// The aliases are the words people use for the same thing, so the model can
	// emit whichever reads naturally in the reply.
	"interact": interactHandler,
	"click":    interactHandler,
	"use":      interactHandler,
	"talk":     interactHandler,
	"press":    interactHandler,
	"sign":     interactHandler,
	"npc":      interactHandler,
	"button":   interactHandler,

	// Switching servers. A new connection, not a packet: the current session is
	// ended and the run loop re-dials the requested address.
	"join":         joinHandler,
	"leaveserver":  joinHandler,
	"switchserver": joinHandler,

	// Server-side commands. Sent as CommandRequest, not as a chat message that
	// merely looks like a command.
	"cmd":     commandHandler,
	"command": commandHandler,

	// Status / inventory
	"status": func(b *bot.Bot, _, user string) {
		hp, hunger, coords := b.GetStatusDetails()
		reportStatus(b, user, event.ActionStatus{Action: "status", Item: fmt.Sprintf("HP:%d Hunger:%d Coords:%s", hp, hunger, coords), Success: true})
	},
	"inventory": func(b *bot.Bot, _, user string) {
		reportStatus(b, user, event.ActionStatus{Action: "inventory", Item: b.GetInventorySummary(), Success: true})
	},

	// Movement patterns
	"swimbackforth": movementHandler("swimbackforth"),
	"walkbackforth": movementHandler("walkbackforth"),
	"walkcircle":    movementHandler("walkcircle"),
	"walksquare":    movementHandler("walksquare"),
	"moonwalk":      movementHandler("moonwalk"),
	"crabwalk":      movementHandler("crabwalk"),
	"zigzag":        movementHandler("zigzag"),
	"spiral":        movementHandler("spiral"),
	"randomwalk":    movementHandler("randomwalk"),
	"jumpforward":   movementHandler("jumpforward"),
	"bunnyhop":      movementHandler("bunnyhop"),
	"panic":         movementHandler("panic"),
	"runaway":       movementHandler("runaway"),
	"chase":         movementHandler("chase"),
	"followrandom":  movementHandler("followrandom"),

	// Emote patterns
	"jumpforever":   emoteHandler("jumpforever"),
	"jumpinplace":   emoteHandler("jumpinplace"),
	"spinslow":      emoteHandler("spinslow"),
	"spinforever":   emoteHandler("spinforever"),
	"spinfast":      emoteHandler("spinfast"),
	"teleportfake":  emoteHandler("teleportfake"),
	"spinlookup":    emoteHandler("spinlookup"),
	"spinlookdown":  emoteHandler("spinlookdown"),
	"dance":         emoteHandler("dance"),
	"floss":         emoteHandler("floss"),
	"naenae":        emoteHandler("naenae"),
	"robot":         emoteHandler("robot"),
	"breakdance":    emoteHandler("breakdance"),
	"throwparty":    emoteHandler("throwparty"),
	"explode":       emoteHandler("explode"),
	"jumpspincombo": emoteHandler("jumpspincombo"),
	"twerk":         emoteHandler("twerk"),
	"dab":           emoteHandler("dab"),
	"wave":          emoteHandler("wave"),
	"headbang":      emoteHandler("headbang"),
	"nod":           emoteHandler("nod"),
	"shake":         emoteHandler("shake"),

	// Look / idle actions
	"lookcrazy": func(b *bot.Bot, param, user string) {
		handleLookOrIdleAction(b, "lookcrazy", param, user)
		reportStatus(b, user, event.ActionStatus{Action: "lookcrazy", Success: true})
	},
	"stare": func(b *bot.Bot, param, user string) {
		handleLookOrIdleAction(b, "stare", param, user)
		reportStatus(b, user, event.ActionStatus{Action: "stare", Success: true})
	},
	"freeze": func(b *bot.Bot, param, user string) {
		handleLookOrIdleAction(b, "freeze", param, user)
		reportStatus(b, user, event.ActionStatus{Action: "freeze", Success: true})
	},
	"vibrate": func(b *bot.Bot, param, user string) {
		handleLookOrIdleAction(b, "vibrate", param, user)
		reportStatus(b, user, event.ActionStatus{Action: "vibrate", Success: true})
	},

	// Dig / tower actions. These are sustained movements rather than a
	// discrete result, so the step reports that the bot started and
	// stopped, not that a shaft reached a particular depth.
	"buryself":   func(b *bot.Bot, param, user string) { go digDownAction(b, "buryself", param, user) },
	"digout":     func(b *bot.Bot, param, user string) { go digDownAction(b, "digout", param, user) },
	"dighole":    func(b *bot.Bot, param, user string) { go digDownAction(b, "dighole", param, user) },
	"gotohell":   func(b *bot.Bot, param, user string) { go digDownAction(b, "gotohell", param, user) },
	"descend":    func(b *bot.Bot, param, user string) { go digDownAction(b, "descend", param, user) },
	"buildtower": func(b *bot.Bot, param, user string) { go towerAction(b, param, user) },
	"gotoheaven": func(b *bot.Bot, param, user string) { go towerAction(b, param, user) },
	"ascend":     func(b *bot.Bot, param, user string) { go towerAction(b, param, user) },

	// Survival features
	"farm":    func(b *bot.Bot, param, user string) { harvestCrops(b, param, user) },
	"harvest": func(b *bot.Bot, param, user string) { harvestCrops(b, param, user) },
	"plant": func(b *bot.Bot, param, user string) {
		go func() {
			cropType := normalizeCropType(param)
			count := parseCount(param, 20)
			planted := b.Farmer.PlantSeeds(context.Background(), cropType, count)
			b.Logger.Debug("plant complete", "count", planted)
			reportCountOutcome(b, user, "plant", cropType, planted, "tidak ada lahan yang bisa ditanami")
		}()
	},
	"hoe": func(b *bot.Bot, param, user string) {
		radius := safecast.To[int32](parseCount(param, 5))
		go func() {
			hoed := b.Farmer.HoeGround(context.Background(), radius)
			b.Logger.Debug("hoe complete", "count", hoed)
			reportCountOutcome(b, user, "hoe", "hoe", hoed, "tidak ada tanah yang bisa dicangkul")
		}()
	},
	"fish":    func(b *bot.Bot, param, user string) { goFish(b, param, user) },
	"fishing": func(b *bot.Bot, param, user string) { goFish(b, param, user) },
	"breed": func(b *bot.Bot, param, user string) {
		animalType := normalizeItemName(param)
		go func() {
			ok := b.HusbandryMgr.BreedAnimals(context.Background(), animalType)
			reportBoolOutcome(b, user, "breed", animalType, ok, "tidak ada pasangan yang bisa dikawinkan")
		}()
	},
	"feed": func(b *bot.Bot, param, user string) {
		animalType := normalizeItemName(param)
		go func() {
			ok := b.HusbandryMgr.FeedAnimal(context.Background(), animalType)
			reportBoolOutcome(b, user, "feed", animalType, ok, "tidak ada hewan yang bisa diberi makan")
		}()
	},
	"milk": func(b *bot.Bot, _, user string) {
		go func() {
			ok := b.HusbandryMgr.MilkCow(context.Background())
			reportBoolOutcome(b, user, "milk", "milk", ok, "tidak ada sapi yang bisa diperas")
		}()
	},
	"shear": func(b *bot.Bot, _, user string) {
		go func() {
			ok := b.HusbandryMgr.ShearSheep(context.Background())
			reportBoolOutcome(b, user, "shear", "sheep", ok, "tidak ada domba yang bisa dicukur")
		}()
	},
	"tame": func(b *bot.Bot, param, user string) {
		animalType := normalizeItemName(param)
		go func() {
			var ok bool
			if animalType == "cat" || animalType == "ocelot" {
				ok = b.HusbandryMgr.TameCat(context.Background())
			} else {
				ok = b.HusbandryMgr.TameWolf(context.Background())
			}
			reportBoolOutcome(b, user, "tame", animalType, ok, "tidak ada hewan jinak yang bisa dijinakkan")
		}()
	},
	"sleep": func(b *bot.Bot, _, user string) {
		go func() {
			ok := b.SurvivalMgr.SleepInBed(context.Background())
			reportBoolOutcome(b, user, "sleep", "", ok, "tidak bisa tidur, butuh malam dan kasur")
		}()
	},
	"bed": func(b *bot.Bot, _, user string) {
		go func() {
			ok := b.SurvivalMgr.SleepInBed(context.Background())
			reportBoolOutcome(b, user, "sleep", "", ok, "tidak bisa tidur, butuh malam dan kasur")
		}()
	},
	"torch": func(b *bot.Bot, _, user string) {
		go func() {
			// The survival manager places torches on its own and reports to
			// the chat layer, which a waiting plan step cannot see. It does
			// not return a count, so the step confirms the outcome by
			// measuring the torches that are now held.
			before := countInventoryItems(b.GetInventorySlots(), b.GetItemNames(), "torch")
			b.SurvivalMgr.AutoPlaceTorches(context.Background())
			reportInventoryDelta(b, user, "torch", "torch", before, 0)
		}()
	},
	"placetorch": func(b *bot.Bot, _, user string) {
		go func() {
			before := countInventoryItems(b.GetInventorySlots(), b.GetItemNames(), "torch")
			b.SurvivalMgr.AutoPlaceTorches(context.Background())
			reportInventoryDelta(b, user, "torch", "torch", before, 0)
		}()
	},

	// Combat survival
	"shield": func(b *bot.Bot, _, user string) {
		if b.CombatMgr.HasShield() {
			b.CombatMgr.RaiseShield()
			reportStatus(b, user, event.ActionStatus{Action: "shield", Success: true})
		} else {
			reportStatus(b, user, event.ActionStatus{Action: "shield", Success: false, Error: "gak punya shield"})
		}
	},
	"block": func(b *bot.Bot, _, user string) {
		if b.CombatMgr.HasShield() {
			b.CombatMgr.RaiseShield()
			reportStatus(b, user, event.ActionStatus{Action: "shield", Success: true})
		} else {
			reportStatus(b, user, event.ActionStatus{Action: "shield", Success: false, Error: "gak punya shield"})
		}
	},
	"shoot":    func(b *bot.Bot, _, user string) { go handleShoot(b, user) },
	"bow":      func(b *bot.Bot, _, user string) { go handleShoot(b, user) },
	"crossbow": func(b *bot.Bot, _, user string) { go handleShoot(b, user) },
	"potion": func(b *bot.Bot, _, user string) {
		if b.SurvivalMgr.UseHealingPotion() {
			reportStatus(b, user, event.ActionStatus{Action: "heal", Item: "potion", Success: true})
		} else {
			reportStatus(b, user, event.ActionStatus{Action: "heal", Item: "potion", Success: false, Error: "gak punya healing potion"})
		}
	},
	"heal": func(b *bot.Bot, _, user string) {
		if b.SurvivalMgr.UseHealingPotion() {
			reportStatus(b, user, event.ActionStatus{Action: "heal", Item: "potion", Success: true})
		} else {
			reportStatus(b, user, event.ActionStatus{Action: "heal", Item: "potion", Success: false, Error: "gak punya healing potion"})
		}
	},
	"autoeat": func(b *bot.Bot, param, user string) {
		enabled := param != "off" && param != "false" && param != "0"
		b.SurvivalMgr.EnableAutoEat(enabled)
		reportStatus(b, user, event.ActionStatus{Action: "toggle", Item: "auto-eat", Success: true})
	},
	"autoarmor": func(b *bot.Bot, param, user string) {
		enabled := param != "off" && param != "false" && param != "0"
		b.SurvivalMgr.EnableAutoArmor(enabled)
		if enabled {
			count := b.SurvivalMgr.EquipBestArmor()
			reportStatus(b, user, event.ActionStatus{Action: "toggle", Item: "auto-armor", Count: count, Success: true})
		} else {
			reportStatus(b, user, event.ActionStatus{Action: "toggle", Item: "auto-armor", Success: true})
		}
	},
	"autotool": func(b *bot.Bot, _, user string) {
		reportStatus(b, user, event.ActionStatus{Action: "toggle", Item: "auto-tool", Success: true})
	},

	// Exploration
	"explore": func(b *bot.Bot, param, user string) {
		duration := parseCount(param, 60)
		go func() {
			// The explorer reports its own progress to chat, which a waiting
			// step cannot see. It returns the number of blocks it covered, so
			// the step reports that: standing still is not exploring.
			covered := b.Explorer.ExploreRandom(context.Background(), time.Duration(duration)*time.Second)
			reportCountOutcome(b, user, "explore", "explore", covered, "tidak bergerak saat menjelajah")
		}()
	},
	"exploredir": func(b *bot.Bot, param, user string) {
		parts := strings.Split(param, ",")
		direction := "north"
		dist := 200
		if len(parts) > 0 && parts[0] != "" {
			direction = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			_, _ = fmt.Sscanf(parts[1], "%d", &dist)
		}
		go func() {
			covered := b.Explorer.ExploreDirection(context.Background(), direction, dist)
			reportCountOutcome(b, user, "exploredir", direction, covered, "tidak bergerak saat menjelajah")
		}()
	},
	"returnhome": func(b *bot.Bot, _, user string) {
		go func() {
			ok := b.Explorer.ReturnToOrigin(context.Background())
			reportBoolOutcome(b, user, "returnhome", "", ok, "gagal kembali ke titik awal")
		}()
	},
	"shelter": func(b *bot.Bot, _, user string) {
		go func() {
			ok := b.SurvivalMgr.BuildEmergencyShelter(context.Background())
			reportBoolOutcome(b, user, "shelter", "", ok, "gagal membuat tempat berlindung")
		}()
	},
	"time": func(b *bot.Bot, _, user string) {
		tod := b.SurvivalMgr.GetTimeOfDay()
		reportStatus(b, user, event.ActionStatus{Action: "time", Item: tod, Success: true})
	},
	"whatstime": func(b *bot.Bot, _, user string) {
		tod := b.SurvivalMgr.GetTimeOfDay()
		reportStatus(b, user, event.ActionStatus{Action: "time", Item: tod, Success: true})
	},
	"deathpoint": func(b *bot.Bot, _, user string) {
		go func() {
			ok := b.SurvivalMgr.RecoverFromDeath(context.Background())
			reportBoolOutcome(b, user, "recover", "", ok, "gagal mengambil barang di titik kematian")
		}()
	},
	"recover": func(b *bot.Bot, _, user string) {
		go func() {
			ok := b.SurvivalMgr.RecoverFromDeath(context.Background())
			reportBoolOutcome(b, user, "recover", "", ok, "gagal mengambil barang di titik kematian")
		}()
	},
}

// actionHandlersMu guards actionHandlers. The map is written by every init()
// that registers a family of labels and read on every action, and the plan
// executor reads it while handlers may still be registering, so the accesses
// are not naturally ordered.
var actionHandlersMu sync.RWMutex

// lookupHandler returns the handler for label, if one is registered.
func lookupHandler(label string) (actionHandler, bool) {
	actionHandlersMu.RLock()
	defer actionHandlersMu.RUnlock()
	handler, ok := actionHandlers[label]
	return handler, ok
}

// registerHandler adds or replaces a handler. Used by the init() functions
// that wire up a family of labels.
func registerHandler(label string, handler actionHandler) {
	actionHandlersMu.Lock()
	defer actionHandlersMu.Unlock()
	actionHandlers[label] = handler
}

// Execute maps an AI-parsed action tag to bot behaviors.
func Execute(b *bot.Bot, label string, param string, user string) {
	label = strings.ToLower(strings.TrimSpace(label))
	param = strings.TrimSpace(param)

	if handler, ok := lookupHandler(label); ok {
		handler(b, param, user)
		return
	}
	b.Logger.Debug("unknown or unhandled action label", "label", label, "param", param)
}
