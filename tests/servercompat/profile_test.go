package servercompat_test

import (
	"testing"

	"bedrock-ai/internal/servercompat"
)

func TestDetectHansProjectsAsGeyser(t *testing.T) {
	t.Parallel()

	hosts := []string{
		"hansprojects.my.id",
		"hansprojects.my.id:25621",
		"play.hansprojects.my.id",
		"play.hansprojects.my.id:19132",
	}

	for _, host := range hosts {
		p := servercompat.Detect(host)
		if !p.Geyser {
			t.Errorf("Detect(%q).Geyser = false, want true", host)
		}
		if !p.NoSubChunks {
			t.Errorf("Detect(%q).NoSubChunks = false, want true", host)
		}
		if !p.NoHeldItemEcho {
			t.Errorf("Detect(%q).NoHeldItemEcho = false, want true", host)
		}
		if p.IdleNudge {
			t.Errorf("Detect(%q).IdleNudge = true, want false (disabled by default)", host)
		}
		if !p.SlashCommandFirst {
			t.Errorf("Detect(%q).SlashCommandFirst = false, want true", host)
		}
	}
}

func TestDetectOtherServerProfiles(t *testing.T) {
	t.Parallel()

	venity := servercompat.Detect("play.venity.net:19132")
	if !venity.Venity {
		t.Errorf("Detect(venity).Venity = false, want true")
	}
	if venity.Geyser {
		t.Errorf("Detect(venity).Geyser = true, want false")
	}

	ng := servercompat.Detect("play.nethergames.org:19132")
	if !ng.NetherGames {
		t.Errorf("Detect(nethergames).NetherGames = false, want true")
	}
	if ng.Geyser {
		t.Errorf("Detect(nethergames).Geyser = true, want false")
	}
}
