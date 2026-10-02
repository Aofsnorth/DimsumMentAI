// Command verbprobe drives every verb of the affordance catalogue through the
// real bot, one at a time, and leaves a record of what the gate said about each.
//
// It exists because ordinary play never reaches the catalogue. The model answers
// one question per tick and picks whatever the situation invites, so a session of
// wandering returns to the same handful of verbs over and over — a live run
// recorded zero dispatches across several minutes of play — and "the affordance
// layer was exercised" stayed an impression rather than a fact.
//
// The probe takes the model out of the question. It joins the world as a player
// and speaks the same "!verb <label>" an operator would, so what is measured is
// the real path and not a copy of it: the real catalogue lookup, the real gate,
// the real action registry, the real server, and the outcome the bot reports for
// itself. Each dispatch is logged by the bot under "affordance: verb exercised",
// which is the record this tool exists to produce.
//
// The classification half does not need a world at all and is printed directly:
// every verb with its kind, whether it changes the world, and whether it can
// prove the change. Those are the branches a run has to cover, and a table makes
// an unclassified verb obvious in a way a passing test does not.
//
// Usage:
//
//	go run ./cmd/verbprobe -config configs/bot.yaml
//	go run ./cmd/verbprobe -config configs/bot.yaml -only place_block
//	go run ./cmd/verbprobe -config configs/bot.yaml -delay 2s
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"bedrock-ai/internal/bot/affordance"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/connection"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// probeName is the player the probe joins as. It is named for what it is so the
// bot's log is unambiguous about which actor drove a verb.
const probeName = "VerbProbe"

// heartbeatRate is the PlayerAuthInput rate, matching the bot's own 20Hz. A
// probe at any other rate is a different client than the one that matters, and a
// server that idles out a silent client would fail the probe for the wrong
// reason.
const heartbeatRate = 20

func main() {
	configPath := flag.String("config", "configs/bot.yaml", "path to config file")
	delay := flag.Duration("delay", time.Second, "pause between verbs so each verdict lands on its own line")
	only := flag.String("only", "", "drive a single verb instead of the whole catalogue")
	say := flag.Bool("say", true, "print the classification table for the whole catalogue")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	if *say {
		reportCatalogue()
	}

	driven := catalogue()
	if *only != "" {
		driven = selectOne(*only)
	}
	if len(driven) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to drive")
		os.Exit(1)
	}

	dialer := connection.NewDialer(
		cfg.Server,
		login.IdentityData{Identity: uuid.New().String(), DisplayName: probeName},
		probeClientData(cfg),
	)

	fmt.Printf("target=%s verbs=%d delay=%s\n", cfg.Server.Address(), len(driven), *delay)

	conn, err := dialer.Dial()
	if err != nil {
		fmt.Fprintf(os.Stderr, "DIAL FAILED: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.DoSpawn(); err != nil {
		fmt.Fprintf(os.Stderr, "SPAWN FAILED: %v\n", err)
		os.Exit(1)
	}

	gd := conn.GameData()
	fmt.Printf("spawned at %.1f %.1f %.1f\n", gd.PlayerPosition.X(), gd.PlayerPosition.Y(), gd.PlayerPosition.Z())

	// The heartbeat runs for the life of the probe. Without it the client is
	// silent, and a server that drops idle players fails the run before a single
	// verb has been spoken.
	done := make(chan struct{})
	defer close(done)
	go heartbeat(conn, gd, done)

	for i, v := range driven {
		line := "!verb " + v.Label
		if err := conn.WritePacket(&packet.Text{
			TextType:   packet.TextTypeChat,
			SourceName: probeName,
			Message:    line,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "SEND FAILED on %q: %v\n", v.Label, err)
			continue
		}
		fmt.Printf("[%2d/%d] sent %q (%s, %s)\n",
			i+1, len(driven), line, kindName(v.Kind), v.Confirmation)
		time.Sleep(*delay)
	}

	// A last beat so the final verdict is written before the connection drops
	// and takes the bot's reader with it.
	time.Sleep(*delay)
	fmt.Printf("done: drove %d verb(s); read the bot log for \"affordance: verb exercised\"\n", len(driven))
}

// catalogue returns the verbs to drive, in the catalogue's own order so a run is
// comparable with the last one.
func catalogue() []affordance.Verb {
	out := make([]affordance.Verb, len(affordance.Catalogue))
	copy(out, affordance.Catalogue)
	return out
}

// selectOne narrows to a single verb, failing loudly when the name is wrong. A
// probe that silently drove nothing would report a clean run.
func selectOne(label string) []affordance.Verb {
	v, ok := affordance.Lookup(strings.ToLower(strings.TrimSpace(label)))
	if !ok {
		fmt.Fprintf(os.Stderr, "no such verb: %q\n", label)
		os.Exit(1)
	}
	return []affordance.Verb{v}
}

// kindName names a Kind for the report. Kind has no String method of its own,
// and a column of raw iota values reads as noise in a table meant to be scanned.
func kindName(k affordance.Kind) string {
	if k == affordance.Action {
		return "action"
	}
	return "activity"
}

// reportCatalogue prints the classification of every verb and the gate's verdict
// for the whole set.
//
// The gate is asked about the full catalogue on purpose. Asked one verb at a time
// it can only ever answer "kept", and the refusal branch — the rule that an
// unverifiable verb may never be offered for a world-changing intent — would go
// unobserved for every verb the run happened not to touch.
func reportCatalogue() {
	kept, refused := affordance.Gate(affordance.Catalogue)

	fmt.Printf("catalogue: %d verbs, %d offerable, %d held back by the gate\n\n",
		len(affordance.Catalogue), len(kept), len(refused))

	fmt.Println("  verb                                   kind       world  confirms")
	for _, v := range affordance.Catalogue {
		world := "self"
		if v.ChangesWorld {
			world = "WORLD"
		}
		fmt.Printf("  %-36s %-10s %-6s %s\n", v.Label, kindName(v.Kind), world, v.Confirmation)
	}
	fmt.Println()

	if len(refused) > 0 {
		fmt.Println("held back by the gate (unverifiable, and the verb changes the world):")
		for _, v := range refused {
			fmt.Printf("  %-36s %s\n", v.Label, v.Confirmation)
		}
		fmt.Println()
	}
}

// probeClientData is a realistic client description: the bot's own
// mergeClientData reports Android and touch, and a proxy can gate on what the
// client claims to be.
func probeClientData(cfg *config.Config) login.ClientData {
	cd := login.ClientData{
		CurrentInputMode: 2,
		DefaultInputMode: 2,
		DeviceModel:      "SM-G973F",
		DeviceOS:         1,
		GameVersion:      protocol.CurrentVersion,
		LanguageCode:     "en_US",
		UIProfile:        0,
		DeviceID:         login.DeviceID(uuid.New().String()),
	}
	if cfg.Bot.Language == "Indonesian" {
		cd.LanguageCode = "id_ID"
	}
	return cd
}

// heartbeat keeps the connection alive at the bot's own tick rate.
func heartbeat(conn *minecraft.Conn, gd minecraft.GameData, done <-chan struct{}) {
	t := time.NewTicker(time.Second / heartbeatRate)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			_ = conn.WritePacket(&packet.PlayerAuthInput{
				Position:  gd.PlayerPosition,
				Yaw:       gd.Yaw,
				Pitch:     gd.Pitch,
				HeadYaw:   gd.Yaw,
				InputMode: 2,
				PlayMode:  0,
				Tick:      0,
				InputData: protocol.NewInputFlags(packet.InputFlagCount),
			})
		}
	}
}
