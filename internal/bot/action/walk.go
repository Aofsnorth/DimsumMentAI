package action

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// handleWalk walks the body N blocks in the direction it is currently facing.
//
// "Jalan 1 block" in a staged routine ("lompat, jalan 1 block, muter") names a
// relative move, not a destination: the player means "one step the way you are
// looking". The target cell is the current position advanced N blocks along the
// facing vector, snapped to the cell grid, and the existing A* navigation does
// the walking — including the step-up/step-down a straight line would need.
//
// Yaw convention matches interact.ForwardVector: forward = (cos, 0, sin) of
// (yaw + 90)°.
func handleWalk(b *bot.Bot, param, user string) {
	blocks := 1
	if trimmed := strings.TrimSpace(param); trimmed != "" {
		if n, err := strconv.Atoi(trimmed); err == nil {
			blocks = n
		}
	}
	if blocks < 1 {
		blocks = 1
	}
	if blocks > 32 {
		// A staged routine asked for a step, not a march across the map, and a
		// number that large is more likely a misparse than an intent. Cap it;
		// a real long walk still has "goto".
		blocks = 32
	}

	go func() {
		pos := b.GetCoords()
		yaw := b.GetYaw()
		rad := (float64(yaw) + 90) * math.Pi / 180
		tx := pos.X() + float32(math.Cos(rad))*float32(blocks)
		tz := pos.Z() + float32(math.Sin(rad))*float32(blocks)
		cellX := int32(math.Floor(float64(tx)))
		cellY := int32(math.Floor(float64(pos.Y())))
		cellZ := int32(math.Floor(float64(tz)))

		if !b.NavigateToBlock(cellX, cellY, cellZ, 1.0) {
			ReportStatus(b, user, event.ActionStatus{
				Action:  "walk",
				Item:    fmt.Sprintf("%d block", blocks),
				Success: false,
				Error:   "gagal nemu jalur ke arah hadap",
			})
			return
		}
		ReportStatus(b, user, event.ActionStatus{
			Action:  "walk",
			Item:    fmt.Sprintf("%d block", blocks),
			Success: true,
		})
	}()
}
