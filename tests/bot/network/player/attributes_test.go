package player_test

import (
	"testing"

	"bedrock-ai/internal/bot/network/player"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestApplyAttributeValuesExtractsHealthAndHunger(t *testing.T) {
	t.Parallel()

	attrs := []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: 14}},
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:player.hunger", Value: 6}},
	}

	health, hunger, hungerSeen := player.ApplyAttributeValues(attrs, 20, 20)
	if health != 14 {
		t.Errorf("health = %d, want 14", health)
	}
	if hunger != 6 {
		t.Errorf("hunger = %d, want 6", hunger)
	}
	if !hungerSeen {
		t.Error("hungerSeen = false, want true when a hunger attribute is present")
	}
}

// TestApplyAttributeValuesKeepsPreviousHungerWhenAbsent is the case that
// mattered: UpdateAttributes only carries changed attributes, so a health-only
// packet must not reset the tracked hunger to zero.
func TestApplyAttributeValuesKeepsPreviousHungerWhenAbsent(t *testing.T) {
	t.Parallel()

	attrs := []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: 20}},
	}

	health, hunger, hungerSeen := player.ApplyAttributeValues(attrs, 13, 9)
	if health != 20 {
		t.Errorf("health = %d, want 20", health)
	}
	if hunger != 9 {
		t.Errorf("hunger = %d, want the previous value 9 when no hunger attribute is sent", hunger)
	}
	if hungerSeen {
		t.Error("hungerSeen = true, want false when the packet carries no hunger attribute")
	}
}

func TestApplyAttributeValuesEmptyKeepsBoth(t *testing.T) {
	t.Parallel()

	health, hunger, hungerSeen := player.ApplyAttributeValues(nil, 17, 11)
	if health != 17 || hunger != 11 {
		t.Errorf("applyAttributeValues(nil, 17, 11) = (%d, %d), want (17, 11)", health, hunger)
	}
	if hungerSeen {
		t.Error("hungerSeen = true for an empty attribute list")
	}
}
