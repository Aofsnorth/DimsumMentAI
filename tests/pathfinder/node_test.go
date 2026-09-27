package pathfinder_test

import (
	"bedrock-ai/internal/bot/pathfinder"
)

import "testing"

func TestNode_Equal_SameCoords(t *testing.T) {
	t.Parallel()
	a := &pathfinder.Node{X: 1, Y: 2, Z: 3}
	b := &pathfinder.Node{X: 1, Y: 2, Z: 3}
	if !a.Equal(b) {
		t.Error("nodes with same coords should be equal")
	}
}

func TestNode_Equal_DifferentCoords(t *testing.T) {
	t.Parallel()
	a := &pathfinder.Node{X: 1, Y: 2, Z: 3}
	b := &pathfinder.Node{X: 1, Y: 2, Z: 4}
	if a.Equal(b) {
		t.Error("nodes with different Z should not be equal")
	}
}

func TestNode_Equal_NilOther(t *testing.T) {
	t.Parallel()
	a := &pathfinder.Node{X: 1, Y: 2, Z: 3}
	if a.Equal(nil) {
		t.Error("node should not equal nil")
	}
}

func TestLinkType_Constants(t *testing.T) {
	t.Parallel()
	if pathfinder.LinkWalk != "walk" {
		t.Errorf("LinkWalk = %q, want %q", pathfinder.LinkWalk, "walk")
	}
	if pathfinder.LinkJump != "jump" {
		t.Errorf("LinkJump = %q, want %q", pathfinder.LinkJump, "jump")
	}
	if pathfinder.LinkStepJump != "step_jump" {
		t.Errorf("LinkStepJump = %q, want %q", pathfinder.LinkStepJump, "step_jump")
	}
	if pathfinder.LinkFall != "fall" {
		t.Errorf("LinkFall = %q, want %q", pathfinder.LinkFall, "fall")
	}
}
