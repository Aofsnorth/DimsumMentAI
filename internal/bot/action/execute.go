package action

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bedrock-ai/internal/bot"

	"github.com/go-gl/mathgl/mgl32"
)

// Execute maps an AI-parsed action tag to bot behaviors
func Execute(b *bot.Bot, label string, param string, user string) {
	label = strings.ToLower(strings.TrimSpace(label))
	param = strings.TrimSpace(param)

	switch label {
	case "build":
		go b.BuilderAgent.Build(context.Background(), user, param)

	case "stopbuild", "stopbuilding":
		b.BuilderAgent.StopBuilding()

	case "undo":
		count := 0
		if param != "" {
			_, _ = fmt.Sscanf(param, "%d", &count)
		}
		go b.BuilderAgent.UndoBuild(context.Background(), count)

	case "come":
		target := param
		if target == "" {
			target = user
		}
		b.ComeToPlayer(target)

	case "follow":
		target := param
		if target == "" {
			target = user
		}
		b.FollowPlayer(target)

	case "goto":
		if param == "" {
			return
		}
		parts := strings.Split(param, ",")
		if len(parts) == 3 {
			var coords [3]float32
			valid := true
			for i, p := range parts {
				var val float32
				if _, err := fmt.Sscanf(strings.TrimSpace(p), "%f", &val); err != nil {
					valid = false
					break
				}
				coords[i] = val
			}
			if valid {
				b.WalkTo(mgl32.Vec3{coords[0], coords[1], coords[2]})
			}
		}

	case "stop", "stay":
		b.Stop()

	case "lookat":
		target := param
		if target == "" {
			target = user
		}
		if !b.LookAtPlayer(target, 5*time.Second) {
			b.Logger.Warn("ExecuteAction: no player found to look at", "target", target)
		}

	case "emote":
		parts := strings.Split(param, ",")
		emoteName := parts[0]
		b.TriggerEmote(emoteName)

	case "flee":
		go runAwayFromPlayer(b, user, 5*time.Second)

	case "attack", "hunt", "pvp", "guard":
		handleAttack(b, param, user)

	case "equip":
		if param != "" {
			_ = b.InventoryMgr.EquipItem(param)
		}

	case "give":
		handleGive(b, param, user)

	case "drop":
		handleDrop(b, param)

	case "eat":
		go func() {
			_ = b.InventoryMgr.Eat(strings.ToLower(strings.TrimSpace(param)))
		}()

	case "loot":
		go func() {
			collected := b.Gatherer.CollectAllDrops(context.Background(), 10.0)
			b.Logger.Debug("loot action complete", "collected", collected)
		}()

	case "gather":
		count := 10
		itemName := "wood"
		parts := strings.Split(param, ",")
		if len(parts) >= 1 && parts[0] != "" {
			itemName = normalizeItemName(parts[0])
		}
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &count)
		}

		if isWoodLike(itemName) {
			go b.Gatherer.GatherWoodType(context.Background(), itemName, count)
		} else {
			go b.Gatherer.GatherBlock(context.Background(), itemName, count)
		}

	case "mine", "automine":
		count := 10
		itemName := "cobblestone"
		parts := strings.Split(param, ",")
		if len(parts) >= 1 && parts[0] != "" {
			itemName = normalizeItemName(parts[0])
		}
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &count)
		}
		// Logs/wood should always use the tree-by-tree chopper, never the
		// per-block scanner — otherwise the bot jumps between adjacent trees
		// without finishing any.
		if isWoodLike(itemName) {
			go b.Gatherer.GatherWoodType(context.Background(), itemName, count)
		} else {
			go b.Gatherer.GatherBlock(context.Background(), itemName, count)
		}

	case "clear", "scan":
		go func() {
			collected := b.Gatherer.CollectAllDrops(context.Background(), 12.0)
			b.Logger.Debug("sweep drops complete", "collected", collected)
		}()

	case "craft":
		handleCraft(b, param)

	case "smelt":
		go func() {
			itemName := normalizeItemName(param)
			success := b.InventoryMgr.Furnace().SmeltItem(context.Background(), itemName)
			b.Logger.Debug("smelt action complete", "success", success, "item", itemName)
		}()

	case "store", "storeall":
		go func() {
			itemName := normalizeItemName(param)
			success := b.InventoryMgr.Chest().StoreItem(context.Background(), itemName, 0)
			b.Logger.Debug("store action complete", "success", success, "item", itemName)
		}()

	case "take", "retrieve":
		handleTake(b, param, user)

	case "status":
		hp, hunger, coords := b.GetStatusDetails()
		b.SendSafeChat(fmt.Sprintf("HP: %d/20, Hunger: %d/20, Posisi: %s", hp, hunger, coords))

	case "inventory":
		b.SendSafeChat(b.GetInventorySummary())

	case "swimbackforth", "walkbackforth", "walkcircle", "walksquare", "moonwalk", "crabwalk",
		"zigzag", "spiral", "randomwalk", "jumpforward", "bunnyhop", "panic", "runaway",
		"chase", "followrandom":
		go runMovementPattern(b, label, param, user)

	case "jumpforever", "jumpinplace":
		b.TriggerEmoteFor("jump", durationTicks(param, 4*time.Second))

	case "spinslow", "spinforever", "spinfast", "teleportfake":
		b.TriggerEmoteFor("spin", durationTicks(param, 5*time.Second))

	case "spinlookup":
		b.LookAt(b.GetCoords().Add(mgl32.Vec3{0, 8, 0}))
		b.TriggerEmoteFor("spin", durationTicks(param, 5*time.Second))

	case "spinlookdown":
		b.LookAt(b.GetCoords().Add(mgl32.Vec3{0, -4, 0}))
		b.TriggerEmoteFor("spin", durationTicks(param, 5*time.Second))

	case "dance", "floss", "naenae", "robot", "breakdance", "throwparty", "explode", "jumpspincombo":
		b.TriggerEmoteFor("spin", durationTicks(param, 6*time.Second))

	case "twerk":
		b.TriggerEmoteFor("sneak", durationTicks(param, 5*time.Second))

	case "dab", "wave":
		b.TriggerEmoteFor("wave", 30)

	case "headbang", "nod":
		b.TriggerEmoteFor("nod", durationTicks(param, 3*time.Second))

	case "shake":
		b.TriggerEmoteFor("shake", durationTicks(param, 3*time.Second))

	case "lookcrazy", "stare", "freeze", "vibrate":
		handleLookOrIdleAction(b, label, param, user)

	case "buryself", "digout", "dighole", "gotohell", "descend":
		go digDownAction(b, label, param)

	case "buildtower", "gotoheaven", "ascend":
		go towerAction(b, param)

	// === NEW SURVIVAL FEATURES ===
	case "farm", "harvest":
		cropType := normalizeCropType(param)
		count := parseCount(param, 20)
		go func() {
			harvested := b.Farmer.HarvestCrops(context.Background(), cropType, count)
			b.Logger.Debug("harvest complete", "count", harvested)
		}()

	case "plant":
		cropType := normalizeCropType(param)
		count := parseCount(param, 20)
		go func() {
			planted := b.Farmer.PlantSeeds(context.Background(), cropType, count)
			b.Logger.Debug("plant complete", "count", planted)
		}()

	case "hoe":
		radius := int32(parseCount(param, 5))
		go func() {
			hoed := b.Farmer.HoeGround(context.Background(), radius)
			b.Logger.Debug("hoe complete", "count", hoed)
		}()

	case "fish", "fishing":
		count := parseCount(param, 5)
		go func() {
			caught := b.Fisher.GoFish(context.Background(), count)
			b.Logger.Debug("fishing complete", "caught", caught)
		}()

	case "breed":
		animalType := normalizeItemName(param)
		go b.HusbandryMgr.BreedAnimals(context.Background(), animalType)

	case "feed":
		animalType := normalizeItemName(param)
		go b.HusbandryMgr.FeedAnimal(context.Background(), animalType)

	case "milk":
		go b.HusbandryMgr.MilkCow(context.Background())

	case "shear":
		go b.HusbandryMgr.ShearSheep(context.Background())

	case "tame":
		animalType := normalizeItemName(param)
		go func() {
			if animalType == "cat" || animalType == "ocelot" {
				b.HusbandryMgr.TameCat(context.Background())
			} else {
				b.HusbandryMgr.TameWolf(context.Background())
			}
		}()

	case "sleep", "bed":
		go func() {
			b.SurvivalMgr.SleepInBed(context.Background())
		}()

	case "torch", "placetorch":
		go b.SurvivalMgr.AutoPlaceTorches(context.Background())

	case "shield", "block":
		if b.CombatMgr.HasShield() {
			b.CombatMgr.RaiseShield()
			b.SendSafeChat("Shield naik! 🛡️")
		} else {
			b.SendSafeChat("Aku gak punya shield.")
		}

	case "shoot", "bow", "crossbow":
		go func() {
			if b.CombatMgr.HasRangedWeapon() {
				// Find nearest hostile mob to shoot
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
					b.SendSafeChat("Gak ada target dalam jarak tembak.")
				}
			} else {
				b.SendSafeChat("Aku gak punya bow/arrow.")
			}
		}()

	case "potion", "heal":
		if b.SurvivalMgr.UseHealingPotion() {
			b.SendSafeChat("Minum potion! ✨")
		} else {
			b.SendSafeChat("Gak punya healing potion.")
		}

	case "autoeat":
		enabled := param != "off" && param != "false" && param != "0"
		b.SurvivalMgr.EnableAutoEat(enabled)
		if enabled {
			b.SendSafeChat("Auto-eat dinyalakan! 🍖")
		} else {
			b.SendSafeChat("Auto-eat dimatikan.")
		}

	case "autoarmor":
		enabled := param != "off" && param != "false" && param != "0"
		b.SurvivalMgr.EnableAutoArmor(enabled)
		if enabled {
			count := b.SurvivalMgr.EquipBestArmor()
			b.SendSafeChat("Auto-armor: equip " + string(rune('0'+count)) + " armor piece.")
		} else {
			b.SendSafeChat("Auto-armor dimatikan.")
		}

	case "autotool":
		b.SendSafeChat("Auto-tool aktif - akan pilih tool terbaik otomatis saat mine.")

	case "explore":
		duration := parseCount(param, 60)
		go b.Explorer.ExploreRandom(context.Background(), time.Duration(duration)*time.Second)

	case "exploredir":
		parts := strings.Split(param, ",")
		direction := "north"
		dist := 200
		if len(parts) >= 1 && parts[0] != "" {
			direction = strings.TrimSpace(parts[0])
		}
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &dist)
		}
		go b.Explorer.ExploreDirection(context.Background(), direction, dist)

	case "returnhome":
		go b.Explorer.ReturnToOrigin(context.Background())

	case "shelter":
		go b.SurvivalMgr.BuildEmergencyShelter(context.Background())

	case "time", "whatstime":
		tod := b.SurvivalMgr.GetTimeOfDay()
		b.SendSafeChat("Sekarang waktu: " + tod)

	case "deathpoint", "recover":
		go b.SurvivalMgr.RecoverFromDeath(context.Background())

	default:
		b.Logger.Debug("unknown or unhandled action label", "label", label, "param", param)
	}
}
