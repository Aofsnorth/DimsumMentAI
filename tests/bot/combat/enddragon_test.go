package combat_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
)

// dragonCase is a mid-fight reading to modify per test case: the dragon perched
// just above the bot with nothing left to shoot. Every case below starts from
// it and changes exactly one thing, so a failure names the thing that changed.
func dragonCase() combat.DragonSituation {
	return combat.DragonSituation{
		BotPosition:       mgl32.Vec3{0, 64, 0},
		DragonPosition:    mgl32.Vec3{2, 66, 0},
		DragonDistance:    2.83,
		Weapon:            combat.WeaponSword,
		HasArrows:         true,
		Health:            20,
		MaxHealth:         20,
		CrystalsRemaining: 0,
	}
}

func TestPlanDragonFight_Decisions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		edit func(*combat.DragonSituation)
		want combat.DragonAction
	}{
		{
			name: "a perched dragon with no crystals left is the melee window",
			edit: func(s *combat.DragonSituation) {},
			want: combat.DragonMeleePerch,
		},
		{
			name: "a flying dragon with no crystals left is not a melee target",
			edit: func(s *combat.DragonSituation) {
				s.DragonPosition = mgl32.Vec3{30, 80, 0}
				s.DragonDistance = 32
			},
			want: combat.DragonReposition,
		},
		{
			name: "a perched dragon out of reach is still not a melee target",
			edit: func(s *combat.DragonSituation) {
				s.DragonDistance = 12
				s.DragonPosition = mgl32.Vec3{12, 66, 0}
			},
			want: combat.DragonReposition,
		},
		{
			name: "a crystal in range outranks a dragon in reach",
			edit: func(s *combat.DragonSituation) {
				s.CrystalsRemaining = 3
				s.NearestCrystalDistance = 11
				s.Weapon = combat.WeaponBow
			},
			want: combat.DragonShootCrystal,
		},
		{
			name: "a crystal outranks a flying dragon too",
			edit: func(s *combat.DragonSituation) {
				s.DragonPosition = mgl32.Vec3{30, 80, 0}
				s.DragonDistance = 32
				s.CrystalsRemaining = 3
				s.NearestCrystalDistance = 11
				s.Weapon = combat.WeaponBow
			},
			want: combat.DragonShootCrystal,
		},
		{
			name: "a dragon directly overhead is a dive even at the fountain",
			edit: func(s *combat.DragonSituation) {
				s.DragonPosition = mgl32.Vec3{0, 80, 0}
				s.DragonDistance = 16
			},
			want: combat.DragonRetreatAndHeal,
		},
		{
			name: "a crossbow shoots a crystal with an empty quiver",
			edit: func(s *combat.DragonSituation) {
				s.CrystalsRemaining = 1
				s.NearestCrystalDistance = 4
				s.Weapon = combat.WeaponCrossbow
				s.HasArrows = false
			},
			want: combat.DragonShootCrystal,
		},
		{
			name: "a bow with no arrows falls back to hitting the crystal in reach",
			edit: func(s *combat.DragonSituation) {
				s.CrystalsRemaining = 1
				s.NearestCrystalDistance = 2
				s.Weapon = combat.WeaponBow
				s.HasArrows = false
			},
			want: combat.DragonMeleePerch,
		},
		{
			name: "a bow with no arrows and no crystal in reach has nothing to do",
			edit: func(s *combat.DragonSituation) {
				s.CrystalsRemaining = 1
				s.NearestCrystalDistance = 25
				s.Weapon = combat.WeaponBow
				s.HasArrows = false
			},
			want: combat.DragonReposition,
		},
		{
			name: "a crystal past shooting range is repositioning, not shooting",
			edit: func(s *combat.DragonSituation) {
				s.CrystalsRemaining = 1
				s.NearestCrystalDistance = combat.CrystalRange + 1
				s.Weapon = combat.WeaponBow
			},
			want: combat.DragonReposition,
		},
		{
			name: "a dragon diving at the bot is a retreat even mid-crystal",
			edit: func(s *combat.DragonSituation) {
				s.CrystalsRemaining = 2
				s.NearestCrystalDistance = 11
				s.Weapon = combat.WeaponBow
				s.DragonPosition = mgl32.Vec3{3, 78, 0}
				s.DragonDistance = 14.2
			},
			want: combat.DragonRetreatAndHeal,
		},
		{
			name: "low health stops the trading even at the fountain",
			edit: func(s *combat.DragonSituation) { s.Health = 7 },
			want: combat.DragonRetreatAndHeal,
		},
		{
			name: "low health stops the crystal shooting too",
			edit: func(s *combat.DragonSituation) {
				s.Health = 3
				s.CrystalsRemaining = 2
				s.NearestCrystalDistance = 11
				s.Weapon = combat.WeaponBow
			},
			want: combat.DragonRetreatAndHeal,
		},
		{
			name: "a bot that cannot read its health does not assume it is dying",
			edit: func(s *combat.DragonSituation) {
				s.Health, s.MaxHealth = 0, 0
			},
			want: combat.DragonMeleePerch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := dragonCase()
			tc.edit(&s)
			if got := combat.PlanDragonFight(s); got != tc.want {
				t.Fatalf("PlanDragonFight() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInHealingBeam(t *testing.T) {
	t.Parallel()

	bot := mgl32.Vec3{0, 64, 0}

	cases := []struct {
		name     string
		bot      mgl32.Vec3
		dragon   mgl32.Vec3
		crystals int
		want     bool
	}{
		{"standing under a circling dragon", bot, mgl32.Vec3{0, 80, 0}, 3, true},
		{"inside the column but off to one side", mgl32.Vec3{2, 64, 0}, mgl32.Vec3{0, 80, 0}, 3, true},
		{"clear of the column", mgl32.Vec3{9, 64, 0}, mgl32.Vec3{0, 80, 0}, 3, false},
		{"a perched dragon is not overhead", bot, mgl32.Vec3{0, 68, 0}, 3, false},
		{"nothing left to heal through, so no beam", bot, mgl32.Vec3{0, 80, 0}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := combat.InHealingBeam(tc.bot, tc.dragon, tc.crystals); got != tc.want {
				t.Fatalf("InHealingBeam() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDragonGeometry(t *testing.T) {
	t.Parallel()

	bot := mgl32.Vec3{0, 64, 0}

	if !combat.IsDragonPerched(bot, mgl32.Vec3{30, 66, 0}) {
		t.Fatal("a dragon at the fountain height counts as perched")
	}
	if combat.IsDragonPerched(bot, mgl32.Vec3{0, 90, 0}) {
		t.Fatal("a dragon thirty blocks up does not count as perched")
	}

	if !combat.IsDragonDiving(bot, mgl32.Vec3{4, 80, 0}) {
		t.Fatal("a dragon close and high is a pass at the bot")
	}
	if combat.IsDragonDiving(bot, mgl32.Vec3{40, 80, 0}) {
		t.Fatal("a dragon far away is a circle, not a dive")
	}
	// The perch check is the one that keeps the melee window open: a dragon
	// sitting at the fountain is close and above the bot, and treating that as
	// a dive would mean the bot never gets a hit in.
	if combat.IsDragonDiving(bot, mgl32.Vec3{2, 66, 0}) {
		t.Fatal("a perched dragon must not read as a dive")
	}
}

func TestEndCrystals_FindsOnlyCrystals(t *testing.T) {
	t.Parallel()

	actors := map[uint64]*entity.Info{
		1: {ID: 1, Type: "minecraft:ender_dragon", Name: "minecraft:ender_dragon", Health: 200},
		2: {ID: 2, Type: "minecraft:end_crystal", Name: "minecraft:end_crystal", Health: 1},
		3: {ID: 3, Type: "minecraft:end_crystal", Name: "minecraft:end_crystal", Health: 0},
		4: {ID: 4, Type: "minecraft:sheep", Name: "minecraft:sheep", Health: 10},
	}
	crystals := combat.EndCrystals(actors)
	if len(crystals) != 1 {
		t.Fatalf("EndCrystals() found %d, want only the one live crystal", len(crystals))
	}
	if crystals[0].ID != 2 {
		t.Fatalf("EndCrystals() = %d, want the live crystal 2", crystals[0].ID)
	}
}

func TestPickCrystal(t *testing.T) {
	t.Parallel()

	bot := mgl32.Vec3{0, 64, 0}
	crystals := []*entity.Info{
		{ID: 7, Position: mgl32.Vec3{20, 64, 0}},
		{ID: 8, Position: mgl32.Vec3{6, 64, 0}},
		{ID: 9, Position: mgl32.Vec3{12, 64, 0}},
	}

	if got := combat.PickCrystal(crystals, bot, combat.CrystalRange); got == nil || got.ID != 8 {
		t.Fatalf("PickCrystal() = %v, want the nearest crystal 8", got)
	}

	// Everything beyond the shooting horizon is ignored rather than walked at.
	if got := combat.PickCrystal(crystals, bot, 5); got != nil {
		t.Fatalf("PickCrystal() within 5 blocks = %v, want nil", got)
	}
	if got := combat.PickCrystal(nil, bot, combat.CrystalRange); got != nil {
		t.Fatalf("PickCrystal() with no crystals = %v, want nil", got)
	}

	// Two crystals the same distance away must resolve the same way every tick,
	// or the bot draws a full bow at each in turn and hits neither.
	tied := []*entity.Info{
		{ID: 30, Position: mgl32.Vec3{10, 64, 0}},
		{ID: 20, Position: mgl32.Vec3{-10, 64, 0}},
	}
	for i := 0; i < 8; i++ {
		if got := combat.PickCrystal(tied, bot, combat.CrystalRange); got == nil || got.ID != 20 {
			t.Fatalf("PickCrystal() tie = %v, want the lower ID 20 every time", got)
		}
	}
}

func TestDragonDestination_StaysOutOfTheHealingBeam(t *testing.T) {
	t.Parallel()

	bot := mgl32.Vec3{0, 64, 0}
	dragon := mgl32.Vec3{4, 80, 0}

	// Already standing under the dragon: the way out wins over the way the
	// action wanted to go.
	out := combat.DragonDestination(bot, dragon, mgl32.Vec3{4, 64, 0}, 2)
	if combat.InHealingBeam(out, dragon, 2) {
		t.Fatalf("destination %v is still inside the beam", out)
	}
	if d := combat.HorizontalDistance(out, dragon); d <= combat.BeamCorridorRadius {
		t.Fatalf("destination is only %v blocks from the dragon's column", d)
	}

	// Standing clear but about to walk into the column: the destination is
	// replaced even though the bot itself is fine.
	clear := mgl32.Vec3{0, 64, 0}
	into := combat.DragonDestination(clear, mgl32.Vec3{22, 80, 0}, mgl32.Vec3{21, 64, 0}, 2)
	if combat.InHealingBeam(into, mgl32.Vec3{22, 80, 0}, 2) {
		t.Fatalf("destination %v walks the bot under the dragon", into)
	}

	// With no crystals left there is no beam to dodge, so the plan stands.
	same := combat.DragonDestination(bot, dragon, mgl32.Vec3{4, 64, 0}, 0)
	if same != (mgl32.Vec3{4, 64, 0}) {
		t.Fatalf("destination = %v, want the preferred point untouched", same)
	}
}

func TestDragonDodgePoint_RetreatsAndSlipsSideways(t *testing.T) {
	t.Parallel()

	bot := mgl32.Vec3{0, 64, 0}
	dragon := mgl32.Vec3{2, 78, 0}

	for _, side := range []float32{1, -1} {
		dodge := combat.DragonDodgePoint(bot, dragon, side)
		if d := combat.HorizontalDistance(dodge, dragon); math.Abs(float64(d-combat.DragonRetreatDistance)) > 1e-3 {
			t.Fatalf("dodge distance = %v, want %v", d, combat.DragonRetreatDistance)
		}
		// Off the straight line: a dodge straight backwards is just a
		// retreat, and a dragon that tracks the bot simply follows it.
		if dodge.Z() == 0 {
			t.Fatalf("dodge %v for side %v is still on the line back", dodge, side)
		}
	}
}

func TestCrystalChoice(t *testing.T) {
	t.Parallel()

	bowOnly := map[uint32]string{0: "minecraft:bow", 1: "minecraft:arrow", 2: "minecraft:iron_sword"}
	if got := combat.CrystalChoice(bowOnly); got.Kind != combat.WeaponBow {
		t.Fatalf("crystalChoice() with only a bow = %v, want a bow", got.Kind)
	}

	both := map[uint32]string{0: "minecraft:bow", 1: "minecraft:crossbow", 2: "minecraft:iron_sword"}
	if got := combat.CrystalChoice(both); got.Kind != combat.WeaponCrossbow {
		t.Fatalf("crystalChoice() = %v, want the crossbow preferred over the bow", got.Kind)
	}

	// A sword is the melee fallback, not a shot: the plan only reaches this
	// function when something can be fired, so the absence has to be honest.
	if got := combat.CrystalChoice(map[uint32]string{0: "minecraft:iron_sword"}); got.Kind != combat.WeaponNone {
		t.Fatalf("crystalChoice() with nothing to shoot = %v, want none", got.Kind)
	}
}

func TestDragonTargetClassification(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"minecraft:end_crystal", "End Crystal", "end_crystal"} {
		if !combat.IsEndCrystal(name) {
			t.Fatalf("IsEndCrystal(%q) = false, want true", name)
		}
	}
	if combat.IsEndCrystal("minecraft:ender_dragon") {
		t.Fatal("IsEndCrystal(ender_dragon) = true, want false")
	}

	for _, name := range []string{"minecraft:ender_dragon", "Ender Dragon", "ender_dragon"} {
		if !combat.IsEnderDragon(name) {
			t.Fatalf("IsEnderDragon(%q) = false, want true", name)
		}
	}
	if combat.IsEnderDragon("minecraft:end_crystal") {
		t.Fatal("IsEnderDragon(end_crystal) = true, want false")
	}
}

func TestCrystalAimPoint_AimsAtTheCrystalAndLiftsWithRange(t *testing.T) {
	t.Parallel()

	bot := mgl32.Vec3{0, 64, 0}
	near := combat.CrystalAimPoint(bot, mgl32.Vec3{2, 64, 0})
	far := combat.CrystalAimPoint(bot, mgl32.Vec3{20, 64, 0})

	// A crystal is a small target sitting on its pillar, so the aim is the
	// middle of it rather than the 1.2-block body centre a mob gets.
	if near.Y() < 64.9 || near.Y() > 65.3 {
		t.Fatalf("near crystal aim height = %v, want the middle of the crystal", near.Y())
	}
	if far.Y() <= near.Y() {
		t.Fatalf("far crystal aim height = %v, want above the near aim %v", far.Y(), near.Y())
	}
}
