// Two raw body pushes that bypass pathfinding entirely.
//
// applyKnockback folds the horizontal motion the server assigned via
// SetActorMotion into the claimed position. Bedrock clients integrate
// server-assigned motion themselves, so a body that keeps its X/Z through a
// hit is a body the server never saw slide — read from the outside as an
// anti-knockback cheat.
//
// The shape of that integration is what makes it read as a hit rather than a
// walk. Two properties have to hold:
//
//   - Ground friction, not air drag. The vanilla client drags carried motion
//     at 0.91 × 0.6 slipperiness ≈ 0.55 per tick on the ground, so an assigned
//     velocity slides the body about one velocity-length and stops within
//     a third of a second — the jolt. Dragging at the air rate (0.91) instead
//     stretched the same hit into an eleven-fold glide over two seconds,
//     which is not a knockback, it is a stroll in the direction of the punch
//     — and it also diverged from the server's own simulation for 1.7s,
//     inviting corrections that chopped the slide mid-way.
//   - The look stays put. A hit player does not turn to face the direction
//     they were shoved; the slide happens under a body that keeps facing
//     what it was facing. Declaring the shove as intentional movement
//     (HasHorizontalMove) handed the head to the movement look pipeline and
//     turned the body into the push — the "jalan ke arah knockback" report.
//
// applyForceWalk is the player-ordered push: walk straight along a yaw for a
// few seconds, no route. It exists because A* can refuse to route at all —
// terrain it cannot model, a blocked bridge — and a bot that then stands
// still saying nothing is exactly the "bot nggak bisa jalan" complaint. It
// deliberately clears the active route instead of negotiating with it.

package movement

import (
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// Knockback friction per tick: ground matches vanilla (0.91 drag × 0.6
// block slipperiness), air keeps the bare 0.91, and carried motion below
// knockbackZero stops being worth claiming.
const (
	groundKnockbackFriction float32 = 0.546
	airKnockbackFriction    float32 = 0.91
	knockbackZero           float32 = 0.01
)

// applyKnockback claims one tick of server-assigned horizontal motion.
//
// It runs after the physics phase and before the position claim, so the
// displacement is inside the very next PlayerAuthInput: the server sees a
// body that actually slid, without the body's look or input flags ever
// claiming it wanted to go there.
func (tc *TickContext) applyKnockback() bool {
	tc.B.Mu.Lock()
	kbX, kbZ := tc.B.KnockbackX, tc.B.KnockbackZ
	if kbX == 0 && kbZ == 0 {
		tc.B.Mu.Unlock()
		return false
	}
	tc.B.KnockbackX, tc.B.KnockbackZ = 0, 0
	tc.B.Mu.Unlock()

	tc.CurrPos = mgl32.Vec3{tc.CurrPos.X() + kbX, tc.CurrPos.Y(), tc.CurrPos.Z() + kbZ}
	tc.B.Logger.Info("knockback folded",
		"vx", kbX, "vz", kbZ,
		"from_x", tc.CurrPos.X()-kbX, "from_z", tc.CurrPos.Z()-kbZ,
		"to_x", tc.CurrPos.X(), "to_z", tc.CurrPos.Z(),
		"grounded", tc.IsGrounded)

	decay := airKnockbackFriction
	if tc.IsGrounded {
		decay = groundKnockbackFriction
	}
	nx, nz := kbX*decay, kbZ*decay
	if absFloat32(nx) < knockbackZero && absFloat32(nz) < knockbackZero {
		return true
	}
	tc.B.Mu.Lock()
	tc.B.KnockbackX, tc.B.KnockbackZ = nx, nz
	tc.B.Mu.Unlock()
	return true
}

// applyForceWalk claims the body while a ForceWalk window is open, steering
// straight along its world-space direction with no path and no A*.
func (tc *TickContext) applyForceWalk() bool {
	tc.B.Mu.Lock()
	until := tc.B.ForceWalkUntil
	dir := tc.B.ForceWalkDir
	tc.B.Mu.Unlock()
	if until.IsZero() || !time.Now().Before(until) {
		return false
	}
	if dir.X() == 0 && dir.Y() == 0 {
		return false
	}

	// The route is the thing that failed. Clearing it keeps the tick's other
	// steering from re-asserting a path the bot has already been told to
	// ignore, and keeps EnsureWalkToHasPath from re-planning mid-push.
	tc.B.Mu.Lock()
	tc.B.CurrentPath = nil
	tc.B.PathIndex = 0
	tc.B.Mu.Unlock()
	tc.HasPath = false

	tc.MState = "force_walk"
	tc.HasHorizontalMove = true
	tc.Dx, tc.Dz = dir.X(), dir.Y()
	tc.Dist = 1.0
	// One-block ledges get the auto-jump: a straight push that face-plants on
	// a curb is not a walk, it is a shove into a wall.
	tc.applyAutoJump(false)
	return true
}
