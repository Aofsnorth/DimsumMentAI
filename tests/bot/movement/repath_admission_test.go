package movement_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Re-planning is an A* search measured in hundreds of milliseconds and it had no
// admission control: a caller asking for a route and the movement tick asking
// whether it has one both started a search for the same destination, and the
// world paid for two searches to get one route.

func newRepathBot() *bot.Bot {
	return &bot.Bot{
		MovementState:       "walk_to",
		TargetPos:           mgl32.Vec3{10.5, 64, 0.5},
		TargetTolerance:     2.0,
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		RecentBotMessages:   make(map[string]time.Time),
		RecentStatusReports: make(map[string]time.Time),
		InventoryMap:        make(map[uint32]protocol.ItemStack),
	}
}

func TestASecondSearchForTheSameDestinationIsRefused(t *testing.T) {
	t.Parallel()

	b := newRepathBot()
	dest := b.TargetPos

	b.Mu.Lock()
	first := b.ClaimRepathLocked(dest)
	b.Mu.Unlock()
	if !first {
		t.Fatal("the first claim was refused, want it admitted")
	}

	b.Mu.Lock()
	second := b.ClaimRepathLocked(dest)
	b.Mu.Unlock()
	if second {
		t.Error("a second search for the same destination was admitted, want it refused while one is running")
	}
}

func TestANewDestinationIsStillAdmittedWhileASearchRuns(t *testing.T) {
	t.Parallel()

	// The reason the guard is per destination rather than a blanket "one at a
	// time". A blanket guard would drop this request, the in-flight search would
	// publish a route to the OLD tree, and nothing would correct it — the bot
	// would then have a path and no reason to ask again.
	b := newRepathBot()

	b.Mu.Lock()
	b.ClaimRepathLocked(b.TargetPos)
	elsewhere := mgl32.Vec3{-4.5, 70, 12.5}
	admitted := b.ClaimRepathLocked(elsewhere)
	b.Mu.Unlock()

	if !admitted {
		t.Error("a search for a different destination was refused, want it admitted")
	}
}

func TestARepathIsAdmittedAgainOnceTheFirstFinishes(t *testing.T) {
	t.Parallel()

	// Otherwise the bot would refuse to plan for the rest of the session after a
	// single search, and the failure would look exactly like a frozen bot.
	b := newRepathBot()
	dest := b.TargetPos

	b.Mu.Lock()
	b.ClaimRepathLocked(dest)
	b.Mu.Unlock()
	b.ReleaseRepath()

	b.Mu.Lock()
	again := b.ClaimRepathLocked(dest)
	b.Mu.Unlock()
	if !again {
		t.Error("a search was refused after the previous one finished")
	}
}

func TestAnIdleBotProducesNoRouteAtAll(t *testing.T) {
	t.Parallel()

	// The guard must not turn a bot with nowhere to go into one that keeps
	// searching: RecalculatePath still returns early for an idle bot, before any
	// search is claimed.
	b := newRepathBot()
	b.MovementState = "idle"
	b.TargetPos = mgl32.Vec3{}

	movement.RecalculatePath(b)

	if len(b.CurrentPath) != 0 {
		t.Fatalf("idle bot produced a %d node route, want none", len(b.CurrentPath))
	}
}
