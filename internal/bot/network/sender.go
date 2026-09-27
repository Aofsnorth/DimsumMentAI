// Package network manages the bot's connection, packet dispatch, and chunk requests.
package network

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/network/world"
	"bedrock-ai/internal/debuglog"

	"github.com/go-gl/mathgl/mgl32"
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

type chunkCoord struct {
	x, z int32
}

// chunkRequestRetry is how long a chunk may go without a refresh request. A
// single SubChunkRequest is fire-and-forget: if it is lost, or the server has
// not generated the column yet, that chunk stays empty forever and the bot
// walks on an all-air world there.
const chunkRequestRetry = 20 * time.Second

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
			dimension := b.ChunkDimension
			limit := b.SubChunkLimit
			b.Mu.Unlock()

			targets := buildChunkTargets(b, pos, mState, tPlayer)
			sent := sendSubChunkRequests(b, targets, requested, world.SubChunkRow(int32(pos.Y())), dimension, limit)
			if sent > 0 {
				// #region agent log
				debuglog.Log("A", "sender.go:ChunkRequesterLoop", "subchunk requests sent", map[string]any{
					"chunks":        sent,
					"offsetsPerReq": len(world.SubChunkOffsets(world.SubChunkRow(int32(pos.Y())), limit)),
					"totalWrites":   sent,
					"runId":         "post-fix",
				})
				// #endregion
			}
		}
	}
}

// buildChunkTargets returns the list of chunk coordinates to request around
// the bot and, when following, around the target player.
func buildChunkTargets(b *bot.Bot, pos mgl32.Vec3, mState, tPlayer string) []chunkCoord {
	chunkX := int32(math.Floor(float64(pos.X()) / 16.0))
	chunkZ := int32(math.Floor(float64(pos.Z()) / 16.0))

	targets := make([]chunkCoord, 0, 25)
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

	return targets
}

// sendSubChunkRequests sends SubChunkRequest packets for unique chunks that
// have not been requested yet, plus any whose last request has gone stale. It
// returns the number of packets sent.
func sendSubChunkRequests(b *bot.Bot, targets []chunkCoord, requested map[string]time.Time, centerRow, dimension, limit int32) int {
	uniqueTargets := make(map[chunkCoord]bool, len(targets))
	for _, tc := range targets {
		uniqueTargets[tc] = true
	}

	now := time.Now()
	sent := 0
	for tc := range uniqueTargets {
		k := fmt.Sprintf("%d,%d", tc.x, tc.z)
		if last, ok := requested[k]; ok && now.Sub(last) < chunkRequestRetry {
			continue
		}
		requested[k] = now

		world.SendSubChunkRequest(b, tc.x, tc.z, centerRow, dimension, limit)
		sent++
	}
	return sent
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
		slog.Int("arm_size", int(b.ProtoSkin.ArmSize)),
	)
}
