package movement

import (
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

func (tc *TickContext) updateDistanceToPlayer() {
	tc.DistToPlayer = 999.0
	if tc.MState == "follow" && tc.TPlayer != "" {
		if _, pPos, ok := tc.B.FindPlayer(tc.TPlayer); ok {
			dxP := pPos.X() - tc.CurrPos.X()
			dzP := pPos.Z() - tc.CurrPos.Z()
			tc.DistToPlayer = float32(math.Sqrt(float64(dxP*dxP + dzP*dzP)))
		}
	}
}

func (tc *TickContext) updateTargetPositionIfFollowing() {
	if tc.MState == "follow" && tc.TPlayer != "" {
		if _, pos, ok := tc.B.FindPlayer(tc.TPlayer); ok {
			playerFeetPos := pos

			tc.B.Mu.Lock()
			dxT := playerFeetPos.X() - tc.B.TargetPos.X()
			dzT := playerFeetPos.Z() - tc.B.TargetPos.Z()
			dyT := playerFeetPos.Y() - tc.B.TargetPos.Y()
			timeSinceRecalc := time.Since(tc.B.LastPathRecalcTime)

			dxP := playerFeetPos.X() - tc.CurrPos.X()
			dzP := playerFeetPos.Z() - tc.CurrPos.Z()
			dPlayer := float32(math.Sqrt(float64(dxP*dxP + dzP*dzP)))
			hDiff := float32(math.Abs(float64(playerFeetPos.Y() - tc.CurrPos.Y())))

			isClose := dPlayer < 2.0 && hDiff < 1.5

			if isClose {
				tc.B.CurrentPath = nil
				tc.B.PathIndex = 0
				tc.B.TargetPos = playerFeetPos
				tc.B.Mu.Unlock()
			} else {
				if (dxT*dxT+dyT*dyT+dzT*dzT > 4.0) && timeSinceRecalc > 500*time.Millisecond {
					tc.B.TargetPos = playerFeetPos
					tc.B.LastPathRecalcTime = time.Now()
					tc.B.Mu.Unlock()
					RecalculatePath(tc.B)
					tc.B.Mu.Lock()
				} else {
					tc.B.TargetPos = playerFeetPos
				}
				tc.TPos = tc.B.TargetPos

				hasPath := len(tc.B.CurrentPath) > 0 && tc.B.PathIndex < len(tc.B.CurrentPath)
				if (!hasPath || tc.B.TicksStuck > 10) && timeSinceRecalc > 800*time.Millisecond {
					tc.B.LastPathRecalcTime = time.Now()
					tc.B.Mu.Unlock()
					RecalculatePath(tc.B)
					tc.B.Mu.Lock()
				}
				tc.B.Mu.Unlock()
			}
		}
	}
}

func (tc *TickContext) resolveNextTarget() {
	tc.B.Mu.Lock()
	tc.HasPath = len(tc.B.CurrentPath) > 0 && tc.B.PathIndex < len(tc.B.CurrentPath)
	if tc.HasPath {
		node := tc.B.CurrentPath[tc.B.PathIndex]
		tc.NextTarget = mgl32.Vec3{float32(node.X) + 0.5, float32(node.Y), float32(node.Z) + 0.5}

		if (node.Action == "mine" || node.Action == "place") && !tc.B.ScaffoldingActive {
			tc.B.ScaffoldingActive = true
			go ExecuteScaffoldAction(tc.B, node)
		}
	} else {
		tc.NextTarget = tc.TPos
	}
	tc.B.Mu.Unlock()

	if !tc.HasPath {
		tc.NextTarget = tc.TPos
		tc.B.Mu.Lock()
		tc.B.CurrentPath = nil
		tc.B.Mu.Unlock()
	}

	tc.Dx = tc.NextTarget.X() - tc.CurrPos.X()
	tc.Dz = tc.NextTarget.Z() - tc.CurrPos.Z()
	tc.Dist = float32(math.Sqrt(float64(tc.Dx*tc.Dx + tc.Dz*tc.Dz)))

	tc.PrevPos = tc.CurrPos

	tc.AllowDirectSteering = false
	if tc.HasPath {
		tc.AllowDirectSteering = true
	} else {
		var distanceToTarget float32 = 999.0
		if tc.MState == "follow" && tc.TPlayer != "" {
			distanceToTarget = tc.DistToPlayer
		} else {
			distanceToTarget = tc.Dist
		}

		var hDiffToTarget float32 = 0.0
		if tc.MState == "follow" && tc.TPlayer != "" {
			if _, pPos, ok := tc.B.FindPlayer(tc.TPlayer); ok {
				hDiffToTarget = float32(math.Abs(float64(pPos.Y() - tc.CurrPos.Y())))
			}
		} else {
			hDiffToTarget = float32(math.Abs(float64(tc.TPos.Y() - tc.CurrPos.Y())))
		}

		if tc.MState == "walk_to" {
			if distanceToTarget < 16.0 {
				tc.AllowDirectSteering = true
			}
		} else if distanceToTarget < 8.0 && hDiffToTarget < 1.5 {
			tc.AllowDirectSteering = true
		}
	}
}
