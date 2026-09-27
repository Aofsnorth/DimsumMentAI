package action_test

import (
	"testing"

	"bedrock-ai/internal/bot/action"
)

// TestParseNavCoordsAcceptsRealWorldPhrasings covers the ways a player actually
// gives coordinates. The action decides between "these are coordinates" and
// "this is a block name" purely on this parse, so a false positive turns
// "goto:diamond_ore" into a request to walk to a block called
// "diamond_ore,0,0".
func TestParseNavCoordsAcceptsRealWorldPhrasings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want [3]int32
	}{
		{in: "100,64,-20", want: [3]int32{100, 64, -20}},
		{in: " 100 , 64 , -20 ", want: [3]int32{100, 64, -20}},
		{in: "100.5,64,-20", want: [3]int32{100, 64, -20}},
		{in: "-1,0,1", want: [3]int32{-1, 0, 1}},
	}
	for _, tc := range cases {
		got, ok := action.ParseNavCoords(tc.in)
		if !ok {
			t.Errorf("ParseNavCoords(%q) was rejected, want %v", tc.in, tc.want)
			continue
		}
		if got.X() != tc.want[0] || got.Y() != tc.want[1] || got.Z() != tc.want[2] {
			t.Errorf("ParseNavCoords(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestParseNavCoordsRejectsBlockNames is the other half of the same decision:
// anything that is not exactly three numbers is a block name to search for, and
// must not be reported as malformed coordinates.
func TestParseNavCoordsRejectsBlockNames(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"oak_log", "diamond ore", "", "1,2", "1,2,3,4", "a,b,c", "1,,3"} {
		if got, ok := action.ParseNavCoords(in); ok {
			t.Errorf("ParseNavCoords(%q) = %v, want it treated as a block name", in, got)
		}
	}
}

// TestNormaliseBlockNameStripsNamespaces keeps a target that matches what the
// world actually stores. "Minecraft:Oak_Log" and "oak_log" are the same block;
// treating them as different silently finds nothing.
func TestNormaliseBlockNameStripsNamespaces(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"minecraft:oak_log": "oak_log",
		"custom:elevator":   "elevator",
		"  OAK_LOG  ":       "oak_log",
		"oak_log":           "oak_log",
	}
	for in, want := range cases {
		if got := action.NormaliseBlockName(in); got != want {
			t.Errorf("NormaliseBlockName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSupportedLabels_ContainsNavigation guards the wiring, not the logic. A
// handler that exists but is not registered is an action the LLM can emit and
// the bot will silently ignore — which is what a missing label looks like from
// the player's side.
func TestSupportedLabels_ContainsNavigation(t *testing.T) {
	t.Parallel()

	labels := action.SupportedLabels()
	nav := []string{
		"goto", "move",
		"gotoblock", "walkto", "gotonearest",
		"standon", "ontop", "standabove",
		"enterportal", "portal", "usenetherportal",
		"cmd", "command",
	}
	for _, label := range nav {
		if _, ok := labels[label]; !ok {
			t.Errorf("SupportedLabels missing navigation action %q", label)
		}
	}
}
