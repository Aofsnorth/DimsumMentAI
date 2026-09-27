package action

func SupportedLabels() map[string]struct{} {
	labels := []string{
		"build", "stopbuild", "stopbuilding", "undo",
		"come", "follow", "stop", "stay", "flee", "goto", "move",
		// Navigation: go to a block, stand on top of it, or enter a portal.
		// Each is a separate label because each asks for a different arrival
		// spot — standing beside a block is not standing on it.
		"gotoblock", "walkto", "gotonearest",
		"standon", "ontop", "standabove",
		"enterportal", "portal", "usenetherportal",
		"attack", "hunt", "pvp", "guard",
		"equip", "give", "drop", "eat", "loot",
		"gather", "mine", "automine", "clear", "scan",
		"craft", "smelt", "store", "storeall", "take", "retrieve",
		"status", "inventory", "lookat", "look", "emote", "analyze",
		// === MINEPAL PARITY: curated memory + named places ===
		"remember", "recall", "memories", "forget",
		"sethome", "home",
		"swimbackforth", "walkbackforth", "walkcircle", "walksquare", "moonwalk",
		"crabwalk", "zigzag", "spiral", "randomwalk",
		"jumpforever", "jumpforward", "bunnyhop", "jumpinplace", "jumpspincombo",
		"spinforever", "spinfast", "spinslow", "spinlookup", "spinlookdown",
		"dance", "twerk", "floss", "dab", "naenae", "robot", "breakdance",
		"headbang", "nod", "shake", "lookcrazy", "stare", "panic", "freeze", "vibrate",
		"buryself", "digout", "dighole", "buildtower",
		"followrandom", "runaway", "chase", "throwparty",
		"gotoheaven", "gotohell", "explode", "ascend", "descend", "teleportfake",
		// === NEW SURVIVAL FEATURES ===
		"farm", "harvest", "plant", "hoe",
		"fish", "fishing",
		"breed", "feed", "milk", "shear", "tame",
		"sleep", "bed",
		"torch", "placetorch",
		"shield", "block",
		"shoot", "bow", "crossbow",
		"potion", "heal",
		"autoeat", "autoarmor", "autotool",
		"explore", "exploredir", "returnhome",
		"shelter",
		"time", "whatstime",
		"deathpoint", "recover",
		// === LOW-LEVEL WORLD INTERACTION ===
		// Clicking entities (players, NPCs, server buttons/figures) and block
		// entities (doors, levers, chests, signs). Aliases cover the words people
		// actually use for the same action.
		"interact", "click", "use", "talk", "press", "sign", "npc", "button",
		// Switching to a different server. Not a packet — a new connection.
		"join", "leaveserver", "switchserver",
		// Server-side commands via the CommandRequest channel (not chat text).
		"cmd", "command",
	}
	out := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		out[label] = struct{}{}
	}
	return out
}
