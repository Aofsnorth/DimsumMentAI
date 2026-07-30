package main

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	serverAddress = "127.0.0.1:19140"
	probeName     = "OnyxStygian"
	testTimeout   = 2 * time.Minute
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "E2E failed:", err)
		os.Exit(1)
	}
}

func run() error {
	id := uuid.New()
	conn, err := (&minecraft.Dialer{
		IdentityData: login.IdentityData{
			Identity:    id.String(),
			DisplayName: probeName,
		},
		ClientData: login.ClientData{
			DeviceOS:      protocol.DeviceWin10,
			DeviceID:      login.DeviceID(uuid.New().String()),
			SelfSignedID:  uuid.New().String(),
			LanguageCode:  "en_US",
			GameVersion:   protocol.CurrentVersion,
			ServerAddress: serverAddress,
			SkinID:        uuid.New().String(),
			SkinData:      "",
		},
	}).Dial("raknet", serverAddress)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	if err := conn.DoSpawn(); err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	fmt.Println("probe spawned")

	stopInput := make(chan struct{})
	defer close(stopInput)
	go sendInputLoop(conn, stopInput)

	time.Sleep(time.Second)
	if err := conn.WritePacket(&packet.Text{
		TextType:   packet.TextTypeChat,
		SourceName: probeName,
		Message:    "Buat 4 stick",
	}); err != nil {
		return fmt.Errorf("send craft chat: %w", err)
	}
	fmt.Println("craft chat sent")

	deadline := time.Now().Add(testTimeout)
	if err := conn.SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}
	for {
		pk, err := conn.ReadPacket()
		if err != nil {
			if !time.Now().Before(deadline) {
				return nil
			}
			return fmt.Errorf("read packet: %w", err)
		}
		if text, ok := pk.(*packet.Text); ok {
			fmt.Printf("chat: source=%q message=%q type=%d\n", text.SourceName, text.Message, text.TextType)
		}
	}
}

func sendInputLoop(conn *minecraft.Conn, stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second / 20)
	defer ticker.Stop()
	position := conn.GameData().PlayerPosition
	var tick uint64
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			inputData := protocol.NewBitset(packet.PlayerAuthInputBitsetSize)
			inputData.Set(packet.InputFlagBlockBreakingDelayEnabled)
			inputData.Set(packet.InputFlagVerticalCollision)
			if err := conn.WritePacket(&packet.PlayerAuthInput{
				Position:         position,
				InputData:        inputData,
				InputMode:        packet.InputModeTouch,
				PlayMode:         packet.PlayModeNormal,
				InteractionModel: packet.InteractionModelTouch,
				Tick:             tick,
			}); err != nil {
				fmt.Println("input write:", err)
				return
			}
			tick++
		}
	}
}
