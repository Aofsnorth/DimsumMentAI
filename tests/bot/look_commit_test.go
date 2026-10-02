package bot_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot"

	"github.com/go-gl/mathgl/mgl32"
)

// The head wandered away from what the body was breaking.
//
// A live run showed the bot felling a tree while its aim tracked a friend's
// face: `AGI: acknowledged a player` landed in the middle of a chop. The arm
// swung correctly and the crosshair was not on the trunk, which is why the whole
// thing read as a hit animation rather than as mining.
//
// Two independent causes, and both are covered here:
//
//   - the movement tick reapplies a tracked look target every tick for its whole
//     hold (20/s) against the break rhythm's aim at the block (4/s), so the
//     target won every time; and
//   - the vision reflex had no idea the body was committed.

// committedPlanner reports a running plan, standing in for a body inside a task.
// The planner is an interface on purpose; the gatherer is a concrete type and
// so cannot be stubbed without pulling its whole construction into a gaze test.
type committedPlanner struct{ running bool }

func (p *committedPlanner) Run(string, string, []string) {}
func (p *committedPlanner) RunFromChat(string, string)   {}
func (p *committedPlanner) Cancel()                      {}
func (p *committedPlanner) TodoRenderForPrompt() string  { return "" }
func (p *committedPlanner) TodoRenderForChat() string    { return "" }
func (p *committedPlanner) TodoIsActive() bool           { return false }
func (p *committedPlanner) TodoClear()                   {}
func (p *committedPlanner) IsRunning() bool              { return p.running }

const friend = "Arthenyxx"

func botWithPlayer() (*bot.Bot, string) {
	// The maps come from the embedded player tracker, so they are named
	// explicitly rather than assumed: an uninitialised tracker reads as "no such
	// player" and would make every assertion below pass for the wrong reason.
	b := &bot.Bot{PlayerTracker: bot.NewPlayerTracker(), Planner: &committedPlanner{}}
	b.PlayerEntityIDs[friend] = 42
	b.PlayerPositions[42] = mgl32.Vec3{45, 84, 186}
	return b, friend
}

// TestADeliberateAimReleasesTheTrackingTarget is the frequency fix, and it is
// the one that covers every break path at once. Aiming at a point is exactly the
// signal that the head is spoken for, so it has to end the tracking. Without
// this the block aim and the player aim fight on the movement tick and the block
// aim loses.
func TestADeliberateAimReleasesTheTrackingTarget(t *testing.T) {
	t.Parallel()

	b, player := botWithPlayer()
	if !b.LookAtPlayer(player, 4*time.Second) {
		t.Fatal("could not arm the tracked look target on an idle bot")
	}

	// The body now aims at the block it is breaking.
	b.LookAt(mgl32.Vec3{50, 83, 187})

	b.Mu.Lock()
	name, until := b.LookTargetName, b.LookTargetUntil
	b.Mu.Unlock()

	if name != "" || !until.IsZero() {
		t.Errorf("after aiming at a block the tracked target is %q until %v: the "+
			"movement tick will keep pulling the head to the player", name, until)
	}
}

// TestTheVisionReflexDoesNotStealACommittedHead is the second cause: a person
// felling a tree keeps their eyes on the trunk.
func TestTheVisionReflexDoesNotStealACommittedHead(t *testing.T) {
	t.Parallel()

	b, player := botWithPlayer()
	p := b.Planner.(*committedPlanner)

	// Prove the reflex works on this fixture first. Without this the assertion
	// below is satisfied by a bot that can never see a player at all.
	if !b.LookAtPlayer(player, 4*time.Second) {
		t.Fatal("an idle bot refused to arm the look target: the fixture is wrong " +
			"and the committed assertion below would pass for the wrong reason")
	}

	// The body is now inside a task that has claimed its eyes.
	p.running = true

	if b.LookAtPlayer(player, 4*time.Second) {
		t.Error("the vision reflex took the head while the body was committed: " +
			"the bot would swing at the block and look at the player")
	}

	// The refusal has to drop the target armed above as well. Leaving it is the
	// same bug by a shorter route: the movement tick would still hold the head on
	// the player for the rest of the hold.
	b.Mu.Lock()
	name := b.LookTargetName
	b.Mu.Unlock()
	if name != "" {
		t.Errorf("a refused look left the tracked target set to %q: the head stays "+
			"on the player anyway", name)
	}
}

// TestWalkingIsNotACommitment. A bot that turns its head toward whoever is
// talking to it while it walks is correct, and the AGI layer is the one that
// decides when even that is unwanted. The guard must not swallow the whole
// reflex — breaking a block happens while standing still, so motion is not the
// question being asked.
func TestWalkingIsNotACommitment(t *testing.T) {
	t.Parallel()

	b, player := botWithPlayer()
	b.Mu.Lock()
	b.MovementState = "walk_to"
	b.Mu.Unlock()

	if !b.LookAtPlayer(player, 4*time.Second) {
		t.Error("a walking bot refuses to look at a player: the gaze is dead while " +
			"moving, and following is a moving task that needs it")
	}
}

// TestAnIdleBotStillLooksAtPeople keeps the reflex working when it should. The
// fix cannot pass by disabling the gaze.
func TestAnIdleBotStillLooksAtPeople(t *testing.T) {
	t.Parallel()

	b, player := botWithPlayer()
	b.Mu.Lock()
	b.MovementState = "idle"
	b.Mu.Unlock()

	if !b.LookAtPlayer(player, 4*time.Second) {
		t.Error("an idle bot refuses to look at a player standing next to it")
	}
}

// TestLookingAtAPlayerStillArmsTheTrackingTarget is the opposite direction again,
// so the release cannot pass by never arming in the first place.
func TestLookingAtAPlayerStillArmsTheTrackingTarget(t *testing.T) {
	t.Parallel()

	b, player := botWithPlayer()
	if !b.LookAtPlayer(player, 2*time.Second) {
		t.Fatal("the tracked look target did not arm")
	}
	b.Mu.Lock()
	name := b.LookTargetName
	b.Mu.Unlock()
	if name != player {
		t.Errorf("tracked target = %q, want %q", name, player)
	}
}
