package movement

import (
	"bedrock-ai/internal/bot/pathfinder"
)

func (tc *TickContext) detectLadder() {
	tc.IsLadderActive = false
	tc.LadderWallYaw = -999

	if tc.B.WorldModel != nil {
		tc.B.Mu.Lock()
		hasPathForLadder := len(tc.B.CurrentPath) > 0 && tc.B.PathIndex < len(tc.B.CurrentPath)
		var currNode pathfinder.Node
		if hasPathForLadder {
			currNode = tc.B.CurrentPath[tc.B.PathIndex]
		}
		tc.B.Mu.Unlock()

		var checkX, checkY, checkZ int32

		if tc.B.WorldModel.IsLadder(tc.FeetX, tc.FeetY, tc.FeetZ) || tc.B.WorldModel.IsLadder(tc.FeetX, tc.FeetY+1, tc.FeetZ) {
			tc.IsLadderActive = true
			checkX, checkY, checkZ = tc.FeetX, tc.FeetY, tc.FeetZ
		} else if hasPathForLadder {
			if tc.B.WorldModel.IsLadder(currNode.X, currNode.Y, currNode.Z) || tc.B.WorldModel.IsLadder(currNode.X, currNode.Y-1, currNode.Z) {
				tc.IsLadderActive = true
				checkX, checkY, checkZ = currNode.X, currNode.Y, currNode.Z
			}
		}

		if tc.IsLadderActive {
			// Check adjacent blocks to find which one is solid (wall attached to the ladder)
			if tc.B.WorldModel.IsSolid(checkX+1, checkY, checkZ) {
				tc.LadderWallYaw = -90 // East
			} else if tc.B.WorldModel.IsSolid(checkX-1, checkY, checkZ) {
				tc.LadderWallYaw = 90 // West
			} else if tc.B.WorldModel.IsSolid(checkX, checkY, checkZ+1) {
				tc.LadderWallYaw = 0 // South
			} else if tc.B.WorldModel.IsSolid(checkX, checkY, checkZ-1) {
				tc.LadderWallYaw = 180 // North
			}
		}
	}
}
