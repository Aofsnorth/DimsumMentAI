package action

func SupportedLabels() map[string]struct{} {
	labels := []string{
		"build", "stopbuild", "stopbuilding", "undo",
		"come", "follow", "stop", "stay", "flee", "goto",
		"attack", "hunt", "pvp", "guard",
		"equip", "give", "drop", "eat", "loot",
		"gather", "mine", "automine", "clear", "scan",
		"craft", "smelt", "store", "storeall", "take", "retrieve",
		"status", "inventory", "lookat", "emote",
		"swimbackforth", "walkbackforth", "walkcircle", "walksquare", "moonwalk",
		"crabwalk", "zigzag", "spiral", "randomwalk",
		"jumpforever", "jumpforward", "bunnyhop", "jumpinplace", "jumpspincombo",
		"spinforever", "spinfast", "spinslow", "spinlookup", "spinlookdown",
		"dance", "twerk", "floss", "dab", "naenae", "robot", "breakdance",
		"headbang", "nod", "shake", "lookcrazy", "stare", "panic", "freeze", "vibrate",
		"buryself", "digout", "dighole", "buildtower",
		"followrandom", "runaway", "chase", "throwparty",
		"gotoheaven", "gotohell", "explode", "ascend", "descend", "teleportfake",
	}
	out := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		out[label] = struct{}{}
	}
	return out
}
