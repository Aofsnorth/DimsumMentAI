package exploration_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot/exploration"

	"github.com/go-gl/mathgl/mgl32"
)

type waypointBot struct {
	exploration.Bot
	position           mgl32.Vec3
	navigated, stopped bool
}

func (b *waypointBot) GetCoords() mgl32.Vec3 { return b.position }
func (b *waypointBot) NavigateTo(mgl32.Vec3) { b.navigated = true }
func (b *waypointBot) StopMovement()         { b.stopped = true }

func newTestExplorer(b exploration.Bot) *exploration.Explorer {
	e := exploration.NewExplorer(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.SetExploring(true)
	return e
}

// Fast regressions: commands and elapsed time are not evidence of arrival.
func TestWaypointRequiresActualMovement(t *testing.T) {
	t.Parallel()
	origin := mgl32.Vec3{}
	target := mgl32.Vec3{5, 0, 0}
	if exploration.WaypointReached(origin, origin, target) {
		t.Fatal("stationary bot counted")
	}
	if exploration.WaypointReached(origin, origin, origin) {
		t.Fatal("zero distance counted")
	}
	if exploration.WaypointReached(origin, mgl32.Vec3{1, 0, 0}, target) {
		t.Fatal("unfinished route counted")
	}
	if !exploration.WaypointReached(origin, target, target) {
		t.Fatal("arrival rejected")
	}
}

func TestWaypointTimeoutStopsWithoutSuccess(t *testing.T) {
	t.Parallel()
	b := &waypointBot{}
	e := newTestExplorer(b)
	if e.WalkWaypoint(context.Background(), mgl32.Vec3{5, 0, 0}, time.Millisecond) {
		t.Fatal("stalled navigation reported success")
	}
	if !b.navigated || !b.stopped {
		t.Fatal("movement lifecycle incomplete")
	}
}

func TestCancelledWaypointDoesNotStartMovement(t *testing.T) {
	t.Parallel()
	b := &waypointBot{}
	e := newTestExplorer(b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e.WalkWaypoint(ctx, mgl32.Vec3{5, 0, 0}, time.Second) || b.navigated {
		t.Fatal("cancelled action moved")
	}
}
