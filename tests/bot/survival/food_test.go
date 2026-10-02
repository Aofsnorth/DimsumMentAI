package survival_test

import (
	"testing"
	"time"
)

// TestSetHungerIsWhatAutoEatReads is the regression for the missing wire: the
// UpdateAttributes handler recorded hunger on the bot, but the survival manager
// only ever saw the 20 it was constructed with, so auto-eat could never fire.
func TestSetHungerIsWhatAutoEatReads(t *testing.T) {
	t.Parallel()

	m := newTestManager(newFakeBot())
	m.SetHunger(4)

	got := m.HungerLevel()

	if got != 4 {
		t.Fatalf("hungerLevel after SetHunger(4) = %d, want 4", got)
	}
}

// TestAutoEatFiresOnRealHunger is the behaviour the config advertises. The bot
// only has to be told its hunger is low; it should reach for food by itself.
func TestAutoEatFiresOnRealHunger(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	stock(b, 5, 7, "minecraft:bread")

	m := newTestManager(b)
	m.SetHunger(3)

	if !m.ShouldAutoEat() {
		t.Fatal("shouldAutoEat = false at hunger 3 with food in hand, want true")
	}
}

func TestAutoEatDoesNotFireWhenFull(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	stock(b, 5, 7, "minecraft:bread")

	m := newTestManager(b)
	m.SetHunger(18)

	if m.ShouldAutoEat() {
		t.Error("shouldAutoEat = true at hunger 18, want false")
	}
}

// TestAutoEatIsCooldowned stops the 500ms tick from eating a stack per tick.
func TestAutoEatIsCooldowned(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	stock(b, 5, 7, "minecraft:bread")

	m := newTestManager(b)
	m.SetHunger(2)

	if !m.ShouldAutoEat() {
		t.Fatal("first check refused at hunger 2")
	}
	m.MarkLastEat(time.Now())

	if m.ShouldAutoEat() {
		t.Error("second check fired inside the eat cooldown")
	}
}
