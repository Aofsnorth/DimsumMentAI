package servercompat

import (
	"os"
	"strings"
)

// Profile is the set of server-specific behaviours this bot knows about.
//
// Each flag turns on a compatibility shim for one server software or front-end.
// They are detected from the host because the shim is safe on some servers and
// harmful on others — sending extra packets to a server that already works is
// how a fix for one host breaks another.
type Profile struct {
	Venity      bool
	NetherGames bool
	// Geyser marks a server fronted by Geyser (Bedrock↔Java bridge, often
	// behind Floodgate). These drop a client that goes quiet for too long, so
	// the bot has to visibly keep moving — but only there, because a server
	// that is already happy with a motionless bot does not need the steps.
	Geyser bool
	// IdleNudge is the derived behaviour: keep shifting position while idle.
	// It is set for any profile known to time out silent clients, so adding a
	// new such server is one field, not a new special case at the call site.
	IdleNudge bool
	// NoSubChunks disables the sub-chunk requester. Measured on
	// play.hansprojects.my.id (Geyser + Floodgate): a client that sends
	// SubChunkRequest goes silent for ~25s and is dropped, while one that only
	// requests a chunk radius stays connected and runs commands normally.
	NoSubChunks bool
	// NoHeldItemEcho suppresses the MobEquipment echo the bot normally sends
	// after a server-driven inventory update. Measured on
	// play.nexusone.fun (Geyser + Floodgate): the server pushes lobby items
	// whose NBT carries a GeyserHash (a Geyser custom item), and echoing one
	// back in a client-side MobEquipment makes the session go permanently
	// silent within milliseconds, while the same bot on a Geyser server whose
	// inventory stays empty (so no echo is ever sent) stays connected. A real
	// Bedrock client only sends MobEquipment when the player switches slots,
	// so suppressing the echo matches vanilla client behaviour anyway.
	NoHeldItemEcho bool
	// SlashCommandFirst is the order "auto" tries command forms in.
	//
	// A Geyser front-end was measured answering "/login pass" and staying silent
	// for the bare "login pass", so it wants the slash form first. Every other
	// server keeps bare-first: that is the order they were verified against, and
	// flipping it would regress a server that currently works.
	SlashCommandFirst bool
}

func Detect(host string) Profile {
	h := strings.ToLower(strings.TrimSpace(host))
	p := Profile{
		Venity:      strings.Contains(h, "venity.net") || strings.Contains(h, "venity"),
		NetherGames: strings.Contains(h, "nethergames") || strings.Contains(h, "ngmc.co"),
		Geyser:      detectGeyser(h),
	}
	// Only Geyser-fronted servers are known to drop a silent client. A server
	// where the bot already sits fine must keep sitting fine.
	p.IdleNudge = p.Geyser
	p.NoSubChunks = p.Geyser
	p.NoHeldItemEcho = p.Geyser
	p.SlashCommandFirst = p.Geyser
	return p
}

// detectGeyser recognises a Geyser front-end from the host.
//
// A Geyser front-end is almost always reached through a proxy or a DNS name
// rather than the origin, so there is no single suffix. Instead this looks for
// the markers these setups actually use: an explicit port, or a name carrying
// the usual "geyser"/"floodgate"/"bridge" vocabulary. A player can force it on
// with GEYSER_SERVERS in the environment when a custom domain does not match.
func detectGeyser(host string) bool {
	if force := envList("GEYSER_SERVERS"); force != "" && containsToken(force, host) {
		return true
	}
	for _, marker := range []string{"geyser", "floodgate", "netherite"} {
		if strings.Contains(host, marker) {
			return true
		}
	}
	return false
}

func envList(key string) string {
	return strings.ToLower(strings.TrimSpace(os.Getenv(key)))
}

// containsToken reports whether host appears as a whole token in a
// comma-separated list, so "my.gex" does not match a list entry "gex".
func containsToken(list, host string) bool {
	for _, token := range strings.Split(list, ",") {
		if strings.TrimSpace(token) == host {
			return true
		}
	}
	return false
}
