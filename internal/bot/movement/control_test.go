package movement

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"github.com/go-gl/mathgl/mgl32"
)

func TestIdleBlockLookUsesExactBlockAngles(t *testing.T) {
	b := &bot.Bot{
		IdleLookTargetType: "block",
		IdleLookTargetPos:  mgl32.Vec3{0.5, 63, 0.5},
		NextIdleLookChange: time.Now().Add(time.Second),
	}
	tc := &TickContext{
		B:       b,
		CurrPos: mgl32.Vec3{0.5, 64, 0.5},
		Yaw:     0,
		Pitch:   0,
	}

	tc.applyIdleLook()

	if tc.TargetPitch < 80 {
		t.Fatalf("TargetPitch = %v, want direct downward block look", tc.TargetPitch)
	}
}
