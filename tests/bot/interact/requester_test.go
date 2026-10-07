package interact_test

import (
	"testing"

	"bedrock-ai/internal/bot/interact"
)

// The bot walks up to its owner, so the owner is the nearest entity in front.
// "Click the NPC in front of you" must skip them and reach the real NPC.
func TestInFrontSkipsThePlayerWhoAsked(t *testing.T) {
	t.Parallel()

	b := newFakeBot()
	addEntity(b, 8, "minecraft:player", "Synterix", 0.5, 64, 1.0)
	b.players["Synterix"] = 8
	addEntity(b, 22, "minecraft:armor_stand", "Teleport", 0.5, 64, 3.0)

	request := interact.ParseRequest("depan")
	request.Requester = "Synterix"
	target, err := newTestInteractor(b).Resolve(request)
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if target.ID != 22 {
		t.Errorf("target = %v, want the armor stand (22), not the requester", target)
	}
}
