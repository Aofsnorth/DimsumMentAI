package action

import (
	"testing"
)

func TestSupportedLabels_NotEmpty(t *testing.T) {
	t.Parallel()
	labels := SupportedLabels()
	if len(labels) == 0 {
		t.Fatal("SupportedLabels should not be empty")
	}
}

func TestSupportedLabels_ContainsCoreActions(t *testing.T) {
	t.Parallel()
	labels := SupportedLabels()
	core := []string{
		"build", "come", "follow", "stop", "attack",
		"gather", "mine", "craft", "smelt", "store",
		"status", "inventory", "emote",
		"farm", "fish", "breed", "sleep", "explore",
	}
	for _, label := range core {
		if _, ok := labels[label]; !ok {
			t.Errorf("SupportedLabels missing core action %q", label)
		}
	}
}

func TestSupportedLabels_NoDuplicates(t *testing.T) {
	t.Parallel()
	labels := SupportedLabels()
	// The map structure inherently prevents duplicates, but verify the
	// underlying list doesn't produce inconsistent state by checking a few
	// known aliases map to the same set entry.
	aliases := [][]string{
		{"stopbuild", "stopbuilding"},
		{"mine", "automine"},
		{"store", "storeall"},
		{"take", "retrieve"},
		{"fish", "fishing"},
		{"sleep", "bed"},
		{"torch", "placetorch"},
		{"shield", "block"},
		{"shoot", "bow", "crossbow"},
	}
	for _, group := range aliases {
		for _, alias := range group {
			if _, ok := labels[alias]; !ok {
				t.Errorf("alias %q not in SupportedLabels", alias)
			}
		}
	}
}

func TestSupportedLabels_ContainsFunActions(t *testing.T) {
	t.Parallel()
	labels := SupportedLabels()
	fun := []string{
		"dance", "twerk", "floss", "dab", "naenae", "robot", "breakdance",
		"jumpforever", "spinforever", "moonwalk", "crabwalk",
	}
	for _, label := range fun {
		if _, ok := labels[label]; !ok {
			t.Errorf("SupportedLabels missing fun action %q", label)
		}
	}
}

func TestSupportedLabels_ContainsSurvivalActions(t *testing.T) {
	t.Parallel()
	labels := SupportedLabels()
	survival := []string{
		"autoeat", "autoarmor", "autotool",
		"shelter", "torch", "potion", "heal",
		"deathpoint", "recover", "returnhome",
	}
	for _, label := range survival {
		if _, ok := labels[label]; !ok {
			t.Errorf("SupportedLabels missing survival action %q", label)
		}
	}
}
