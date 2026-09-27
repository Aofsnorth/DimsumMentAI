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
	"time"

	"bedrock-ai/internal/bot"
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
func emoteHandler(label string) actionHandler {
	cfg := emoteDefaults[label]
	return func(b *bot.Bot, param, _ string) {
		if cfg.lookAt != (mgl32.Vec3{}) {
			b.LookAt(b.GetCoords().Add(cfg.lookAt))
		}
		ticks := cfg.ticks
		if !cfg.fixed {
			ticks = durationTicks(param, cfg.duration)
		}
		b.TriggerEmoteFor(cfg.name, ticks)
	}
}

// movementHandler returns a handler that runs the movement pattern for label.
func movementHandler(label string) actionHandler {
	return func(b *bot.Bot, param, user string) {
		go runMovementPattern(b, label, param, user)
	}
}

// gatherHandler returns a handler that gathers a block or wood type.
func gatherHandler(defaultItem string) actionHandler {
	return func(b *bot.Bot, param, _ string) {
		parts := strings.Split(param, ",")
		itemName := defaultItem
		if parts[0] != "" {
			itemName = normalizeItemName(parts[0])
		}
		count := bot.DefaultEmoteCount
		if len(parts) > 1 {
			_, _ = fmt.Sscanf(parts[1], "%d", &count)
		}
		if isWoodLike(itemName) {
			go b.Gatherer.GatherWoodType(context.Background(), itemName, count)
		} else {
			go b.Gatherer.GatherBlock(context.Background(), itemName, count)
		}
	}
}

// lootHandler returns a handler that collects all drops within radius and logs msg.
func lootHandler(radius float32, msg string) actionHandler {
	return func(b *bot.Bot, _, _ string) {
		go func() {
			collected := b.Gatherer.CollectAllDrops(context.Background(), radius)
			b.Logger.Debug(msg, "collected", collected)
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

// harvestCrops harvests the crop type described by param.
func harvestCrops(b *bot.Bot, param string) {
	go func() {
		cropType := normalizeCropType(param)
		count := parseCount(param, 20)
		harvested := b.Farmer.HarvestCrops(context.Background(), cropType, count)
		b.Logger.Debug("harvest complete", "count", harvested)
	}()
}

// goFish makes the bot fish count times.
func goFish(b *bot.Bot, param string) {
	count := parseCount(param, 5)
	go func() {
		caught := b.Fisher.GoFish(context.Background(), count)
		b.Logger.Debug("fishing complete", "caught", caught)
	}()
}

// handleShoot finds the nearest hostile mob and fires a ranged weapon.
func handleShoot(b *bot.Bot, user string) {
	if !b.CombatMgr.HasRangedWeapon() {
		b.ReportActionStatus(user, event.ActionStatus{Action: "shoot", Success: false, Error: "gak punya bow atau arrow"})
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
		b.ReportActionStatus(user, event.ActionStatus{Action: "shoot", Success: false, Error: "gak ada target dalam jarak tembak"})
	}
}

// interactHandler clicks whatever the request points at. The parameter is loose
// on purpose: a player name, a thing type ("npc", "sign", "the one in front of
// you"), or empty for whatever the bot is facing.
func interactHandler(b *bot.Bot, param, user string) {
	// Guarded because this runs in its own goroutine: a nil subsystem here is a
	// process-wide crash, not a recoverable error.
	if b.Interactor == nil {
		b.ReportActionStatus(user, event.ActionStatus{
			Action:  "interact",
			Success: false,
			Error:   "bot belum siap, coba lagi sebentar",
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
		b.ReportActionStatus(user, event.ActionStatus{
			Action:  "join",
			Success: false,
			Error:   "butuh alamat server, contoh: join:192.168.1.10:19132",
		})
		return
	}
	if err := b.RequestJoin(address); err != nil {
		b.ReportActionStatus(user, event.ActionStatus{
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
		b.ReportActionStatus(user, event.ActionStatus{
			Action:  "cmd",
			Success: false,
			Error:   "butuh command, contoh: cmd:/register pass pass",
		})
		return
	}
	if err := b.SendCommand(param); err != nil {
		b.ReportActionStatus(user, event.ActionStatus{Action: "cmd", Item: param, Success: false, Error: err.Error()})
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
			b.ReportActionStatus(user, event.ActionStatus{
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
	b.ReportActionStatus(user, event.ActionStatus{
		Action:  "cmd",
		Item:    command,
		Success: true,
		Error:   "",
	})
}

// actionHandlers maps action labels to their handler functions.
var actionHandlers = map[string]actionHandler{
	// Build / undo
	"build":        func(b *bot.Bot, param, user string) { go b.BuilderAgent.Build(context.Background(), user, param) },
	"stopbuild":    func(b *bot.Bot, _, _ string) { b.BuilderAgent.StopBuilding() },
	"stopbuilding": func(b *bot.Bot, _, _ string) { b.BuilderAgent.StopBuilding() },
	"undo": func(b *bot.Bot, param, _ string) {
		count := 0
		if param != "" {
			_, _ = fmt.Sscanf(param, "%d", &count)
		}
		go b.BuilderAgent.UndoBuild(context.Background(), count)
	},

	// Movement / follow
	"come": func(b *bot.Bot, param, user string) {
		target := param
		if target == "" {
			target = user
		}
		b.ComeToPlayer(target)
	},
	"follow": func(b *bot.Bot, param, user string) {
		target := param
		if target == "" {
			target = user
		}
		b.FollowPlayer(target)
	},
	"goto": func(b *bot.Bot, param, user string) { goToCoords(b, param, user) },
	"stop": func(b *bot.Bot, _, _ string) {
		b.Stop()
		if b.Planner != nil {
			b.Planner.Cancel()
		}
	},
	"stay": func(b *bot.Bot, _, _ string) {
		b.Stop()
		if b.Planner != nil {
			b.Planner.Cancel()
		}
	},
	"flee": func(b *bot.Bot, _, user string) { go runAwayFromPlayer(b, user, 5*time.Second) },
	"lookat": func(b *bot.Bot, param, user string) {
		target := param
		if target == "" {
			target = user
		}
		if !b.LookAtPlayer(target, 5*time.Second) {
			b.Logger.Warn("ExecuteAction: no player found to look at", "target", target)
		}
	},

	// Emote
	"emote": func(b *bot.Bot, param, _ string) { parts := strings.Split(param, ","); b.TriggerEmote(parts[0]) },

	// Combat / items
	"attack": handleAttack,
	"hunt":   handleAttack,
	"pvp":    handleAttack,
	"guard":  handleAttack,
	"equip": func(b *bot.Bot, param, _ string) {
		if param != "" {
			go func() {
				if err := b.InventoryMgr.EquipItem(param); err != nil {
					b.Logger.Warn("equip action failed", "item", param, "error", err)
				}
			}()
		}
	},
	"give":           handleGive,
	"drop":           handleDrop,
	"place":          handlePlace,
	"list_craftable": handleListCraftable,
	"eat": func(b *bot.Bot, param, _ string) {
		go func() {
			_ = b.InventoryMgr.Eat(strings.ToLower(strings.TrimSpace(param)))
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
	"smelt": func(b *bot.Bot, param, _ string) {
		go func() {
			itemName := normalizeItemName(param)
			success := b.InventoryMgr.Furnace().SmeltItem(context.Background(), itemName)
			b.Logger.Debug("smelt action complete", "success", success, "item", itemName)
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
		b.ReportActionStatus(user, event.ActionStatus{Action: "status", Item: fmt.Sprintf("HP:%d Hunger:%d Coords:%s", hp, hunger, coords), Success: true})
	},
	"inventory": func(b *bot.Bot, _, user string) {
		b.ReportActionStatus(user, event.ActionStatus{Action: "inventory", Item: b.GetInventorySummary(), Success: true})
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
	"lookcrazy": func(b *bot.Bot, param, user string) { handleLookOrIdleAction(b, "lookcrazy", param, user) },
	"stare":     func(b *bot.Bot, param, user string) { handleLookOrIdleAction(b, "stare", param, user) },
	"freeze":    func(b *bot.Bot, param, user string) { handleLookOrIdleAction(b, "freeze", param, user) },
	"vibrate":   func(b *bot.Bot, param, user string) { handleLookOrIdleAction(b, "vibrate", param, user) },

	// Dig / tower actions
	"buryself":   func(b *bot.Bot, param, _ string) { go digDownAction(b, "buryself", param) },
	"digout":     func(b *bot.Bot, param, _ string) { go digDownAction(b, "digout", param) },
	"dighole":    func(b *bot.Bot, param, _ string) { go digDownAction(b, "dighole", param) },
	"gotohell":   func(b *bot.Bot, param, _ string) { go digDownAction(b, "gotohell", param) },
	"descend":    func(b *bot.Bot, param, _ string) { go digDownAction(b, "descend", param) },
	"buildtower": func(b *bot.Bot, param, _ string) { go towerAction(b, param) },
	"gotoheaven": func(b *bot.Bot, param, _ string) { go towerAction(b, param) },
	"ascend":     func(b *bot.Bot, param, _ string) { go towerAction(b, param) },

	// Survival features
	"farm":    func(b *bot.Bot, param, _ string) { harvestCrops(b, param) },
	"harvest": func(b *bot.Bot, param, _ string) { harvestCrops(b, param) },
	"plant": func(b *bot.Bot, param, _ string) {
		go func() {
			cropType := normalizeCropType(param)
			count := parseCount(param, 20)
			planted := b.Farmer.PlantSeeds(context.Background(), cropType, count)
			b.Logger.Debug("plant complete", "count", planted)
		}()
	},
	"hoe": func(b *bot.Bot, param, _ string) {
		radius := safecast.To[int32](parseCount(param, 5))
		go func() {
			hoed := b.Farmer.HoeGround(context.Background(), radius)
			b.Logger.Debug("hoe complete", "count", hoed)
		}()
	},
	"fish":    func(b *bot.Bot, param, _ string) { goFish(b, param) },
	"fishing": func(b *bot.Bot, param, _ string) { goFish(b, param) },
	"breed": func(b *bot.Bot, param, _ string) {
		animalType := normalizeItemName(param)
		go b.HusbandryMgr.BreedAnimals(context.Background(), animalType)
	},
	"feed": func(b *bot.Bot, param, _ string) {
		animalType := normalizeItemName(param)
		go b.HusbandryMgr.FeedAnimal(context.Background(), animalType)
	},
	"milk":  func(b *bot.Bot, _, _ string) { go b.HusbandryMgr.MilkCow(context.Background()) },
	"shear": func(b *bot.Bot, _, _ string) { go b.HusbandryMgr.ShearSheep(context.Background()) },
	"tame": func(b *bot.Bot, param, _ string) {
		animalType := normalizeItemName(param)
		go func() {
			if animalType == "cat" || animalType == "ocelot" {
				b.HusbandryMgr.TameCat(context.Background())
			} else {
				b.HusbandryMgr.TameWolf(context.Background())
			}
		}()
	},
	"sleep":      func(b *bot.Bot, _, _ string) { go b.SurvivalMgr.SleepInBed(context.Background()) },
	"bed":        func(b *bot.Bot, _, _ string) { go b.SurvivalMgr.SleepInBed(context.Background()) },
	"torch":      func(b *bot.Bot, _, _ string) { go b.SurvivalMgr.AutoPlaceTorches(context.Background()) },
	"placetorch": func(b *bot.Bot, _, _ string) { go b.SurvivalMgr.AutoPlaceTorches(context.Background()) },

	// Combat survival
	"shield": func(b *bot.Bot, _, user string) {
		if b.CombatMgr.HasShield() {
			b.CombatMgr.RaiseShield()
			b.ReportActionStatus(user, event.ActionStatus{Action: "shield", Success: true})
		} else {
			b.ReportActionStatus(user, event.ActionStatus{Action: "shield", Success: false, Error: "gak punya shield"})
		}
	},
	"block": func(b *bot.Bot, _, user string) {
		if b.CombatMgr.HasShield() {
			b.CombatMgr.RaiseShield()
			b.ReportActionStatus(user, event.ActionStatus{Action: "shield", Success: true})
		} else {
			b.ReportActionStatus(user, event.ActionStatus{Action: "shield", Success: false, Error: "gak punya shield"})
		}
	},
	"shoot":    func(b *bot.Bot, _, user string) { go handleShoot(b, user) },
	"bow":      func(b *bot.Bot, _, user string) { go handleShoot(b, user) },
	"crossbow": func(b *bot.Bot, _, user string) { go handleShoot(b, user) },
	"potion": func(b *bot.Bot, _, user string) {
		if b.SurvivalMgr.UseHealingPotion() {
			b.ReportActionStatus(user, event.ActionStatus{Action: "heal", Item: "potion", Success: true})
		} else {
			b.ReportActionStatus(user, event.ActionStatus{Action: "heal", Item: "potion", Success: false, Error: "gak punya healing potion"})
		}
	},
	"heal": func(b *bot.Bot, _, user string) {
		if b.SurvivalMgr.UseHealingPotion() {
			b.ReportActionStatus(user, event.ActionStatus{Action: "heal", Item: "potion", Success: true})
		} else {
			b.ReportActionStatus(user, event.ActionStatus{Action: "heal", Item: "potion", Success: false, Error: "gak punya healing potion"})
		}
	},
	"autoeat": func(b *bot.Bot, param, user string) {
		enabled := param != "off" && param != "false" && param != "0"
		b.SurvivalMgr.EnableAutoEat(enabled)
		b.ReportActionStatus(user, event.ActionStatus{Action: "toggle", Item: "auto-eat", Success: true})
	},
	"autoarmor": func(b *bot.Bot, param, user string) {
		enabled := param != "off" && param != "false" && param != "0"
		b.SurvivalMgr.EnableAutoArmor(enabled)
		if enabled {
			count := b.SurvivalMgr.EquipBestArmor()
			b.ReportActionStatus(user, event.ActionStatus{Action: "toggle", Item: "auto-armor", Count: count, Success: true})
		} else {
			b.ReportActionStatus(user, event.ActionStatus{Action: "toggle", Item: "auto-armor", Success: true})
		}
	},
	"autotool": func(b *bot.Bot, _, user string) {
		b.ReportActionStatus(user, event.ActionStatus{Action: "toggle", Item: "auto-tool", Success: true})
	},

	// Exploration
	"explore": func(b *bot.Bot, param, _ string) {
		duration := parseCount(param, 60)
		go b.Explorer.ExploreRandom(context.Background(), time.Duration(duration)*time.Second)
	},
	"exploredir": func(b *bot.Bot, param, _ string) {
		parts := strings.Split(param, ",")
		direction := "north"
		dist := 200
		if len(parts) > 0 && parts[0] != "" {
			direction = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			_, _ = fmt.Sscanf(parts[1], "%d", &dist)
		}
		go b.Explorer.ExploreDirection(context.Background(), direction, dist)
	},
	"returnhome": func(b *bot.Bot, _, _ string) { go b.Explorer.ReturnToOrigin(context.Background()) },
	"shelter":    func(b *bot.Bot, _, _ string) { go b.SurvivalMgr.BuildEmergencyShelter(context.Background()) },
	"time": func(b *bot.Bot, _, user string) {
		tod := b.SurvivalMgr.GetTimeOfDay()
		b.ReportActionStatus(user, event.ActionStatus{Action: "time", Item: tod, Success: true})
	},
	"whatstime": func(b *bot.Bot, _, user string) {
		tod := b.SurvivalMgr.GetTimeOfDay()
		b.ReportActionStatus(user, event.ActionStatus{Action: "time", Item: tod, Success: true})
	},
	"deathpoint": func(b *bot.Bot, _, _ string) { go b.SurvivalMgr.RecoverFromDeath(context.Background()) },
	"recover":    func(b *bot.Bot, _, _ string) { go b.SurvivalMgr.RecoverFromDeath(context.Background()) },
}

// Execute maps an AI-parsed action tag to bot behaviors.
func Execute(b *bot.Bot, label string, param string, user string) {
	label = strings.ToLower(strings.TrimSpace(label))
	param = strings.TrimSpace(param)

	if handler, ok := actionHandlers[label]; ok {
		handler(b, param, user)
		return
	}
	b.Logger.Debug("unknown or unhandled action label", "label", label, "param", param)
}
