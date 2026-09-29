package exploration

import (
	"context"
	"github.com/go-gl/mathgl/mgl32"
	"testing"
	"time"
)

type waypointBot struct {
	Bot
	position           mgl32.Vec3
	navigated, stopped bool
}

func (b *waypointBot) GetCoords() mgl32.Vec3 { return b.position }
func (b *waypointBot) NavigateTo(mgl32.Vec3) { b.navigated = true }
func (b *waypointBot) StopMovement()         { b.stopped = true }

// Fast regressions: commands and elapsed time are not evidence of arrival.
func TestWaypointRequiresActualMovement(t *testing.T) {
	t.Parallel()
	origin := mgl32.Vec3{}
	target := mgl32.Vec3{5, 0, 0}
	if waypointReached(origin, origin, target) {
		t.Fatal("stationary bot counted")
	}
	if waypointReached(origin, origin, origin) {
		t.Fatal("zero distance counted")
	}
	if waypointReached(origin, mgl32.Vec3{1, 0, 0}, target) {
		t.Fatal("unfinished route counted")
	}
	if !waypointReached(origin, target, target) {
		t.Fatal("arrival rejected")
	}
}

func TestWaypointTimeoutStopsWithoutSuccess(t *testing.T) {
	t.Parallel()
	b := &waypointBot{}
	e := &Explorer{bot: b, isExploring: true}
	if e.walkWaypoint(context.Background(), mgl32.Vec3{5, 0, 0}, time.Millisecond) {
		t.Fatal("stalled navigation reported success")
	}
	if !b.navigated || !b.stopped {
		t.Fatal("movement lifecycle incomplete")
	}
}

func TestCancelledWaypointDoesNotStartMovement(t *testing.T) {
	t.Parallel()
	b := &waypointBot{}
	e := &Explorer{bot: b, isExploring: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e.walkWaypoint(ctx, mgl32.Vec3{5, 0, 0}, time.Second) || b.navigated {
		t.Fatal("cancelled action moved")
	}
}
