package network

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/debuglog"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func SendLoadingScreenDone(b *bot.Bot) {
	_ = b.Conn.WritePacket(&packet.ServerBoundLoadingScreen{
		Type: packet.LoadingScreenTypeStart,
	})
	_ = b.Conn.WritePacket(&packet.ServerBoundLoadingScreen{
		Type: packet.LoadingScreenTypeEnd,
	})
	_ = b.Conn.Flush()
	b.Logger.Debug("sent loading screen packets")
}

func ChunkRequesterLoop(ctx context.Context, b *bot.Bot) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	requested := make(map[string]time.Time)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.Mu.Lock()
			if !b.SubChunkRequestMode {
				b.Mu.Unlock()
				continue
			}
			pos := b.Pos
			mState := b.MovementState
			tPlayer := b.TargetPlayerName
			b.Mu.Unlock()

			chunkX := int32(math.Floor(float64(pos.X()) / 16.0))
			chunkZ := int32(math.Floor(float64(pos.Z()) / 16.0))
			dim := int32(0)

			type chunkCoord struct {
				x, z int32
			}
			var targets []chunkCoord

			for dx := int32(-2); dx <= 2; dx++ {
				for dz := int32(-2); dz <= 2; dz++ {
					targets = append(targets, chunkCoord{chunkX + dx, chunkZ + dz})
				}
			}

			if mState == "follow" && tPlayer != "" {
				if _, pPos, ok := b.FindPlayer(tPlayer); ok {
					pChunkX := int32(math.Floor(float64(pPos.X()) / 16.0))
					pChunkZ := int32(math.Floor(float64(pPos.Z()) / 16.0))
					for dx := int32(-1); dx <= 1; dx++ {
						for dz := int32(-1); dz <= 1; dz++ {
							targets = append(targets, chunkCoord{pChunkX + dx, pChunkZ + dz})
						}
					}
				}
			}

			uniqueTargets := make(map[chunkCoord]bool)
			for _, tc := range targets {
				uniqueTargets[tc] = true
			}

			sent := 0
			for tc := range uniqueTargets {
				k := fmt.Sprintf("%d,%d", tc.x, tc.z)

				if _, ok := requested[k]; ok {
					continue
				}

				requested[k] = time.Now()

				var offsets []protocol.SubChunkOffset
				for y := int32(-4); y <= 25; y++ {
					offsets = append(offsets, protocol.SubChunkOffset{0, int8(y), 0})
				}

				_ = b.Conn.WritePacket(&packet.SubChunkRequest{
					Dimension: dim,
					Position: protocol.SubChunkPos{
						tc.x,
						0,
						tc.z,
					},
					Offsets: offsets,
				})
				sent++
			}
			if sent > 0 {
				// #region agent log
				debuglog.Log("A", "sender.go:ChunkRequesterLoop", "subchunk requests sent", map[string]any{
					"chunks":        sent,
					"offsetsPerReq": 30,
					"totalWrites":   sent,
					"runId":         "post-fix",
				})
				// #endregion
			}
		}
	}
}

func SendPlayerSkin(b *bot.Bot) {
	if len(b.ProtoSkin.SkinData) == 0 {
		return
	}
	b.ProtoSkin.OverrideAppearance = true
	b.ProtoSkin.PrimaryUser = true
	b.ProtoSkin.Trusted = true

	b.Mu.Lock()
	targetUUID := b.PlayerUUID
	b.Mu.Unlock()

	_ = b.Conn.WritePacket(&packet.PlayerSkin{
		UUID: targetUUID,
		Skin: b.ProtoSkin,
	})
	b.Logger.Debug("sent PlayerSkin packet",
		slog.String("uuid", targetUUID.String()),
		slog.Int("skin_data_len", len(b.ProtoSkin.SkinData)),
		slog.String("arm_size", b.ProtoSkin.ArmSize),
	)
}
