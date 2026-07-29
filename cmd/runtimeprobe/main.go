package main

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func main() {
	id := uuid.New()
	conn, err := (&minecraft.Dialer{
		IdentityData: login.IdentityData{
			Identity:    id.String(),
			DisplayName: "OnyxStygian",
		},
		ClientData: login.ClientData{
			DeviceOS:      protocol.DeviceWin10,
			DeviceID:      login.DeviceID(uuid.New().String()),
			SelfSignedID:  uuid.New().String(),
			LanguageCode:  "en_US",
			GameVersion:   protocol.CurrentVersion,
			ServerAddress: "127.0.0.1:19140",
			SkinID:        uuid.New().String(),
			SkinData:      "",
		},
	}).Dial("raknet", "127.0.0.1:19140")
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	if err := conn.DoSpawn(); err != nil {
		panic(err)
	}
	fmt.Println("probe spawned")

	stopInput := make(chan struct{})
	defer close(stopInput)
	go sendInputLoop(conn, stopInput)
	time.Sleep(time.Second)

	if err := conn.WritePacket(&packet.Text{
		TextType:   packet.TextTypeChat,
		SourceName: "OnyxStygian",
		Message:    "Buat 4 stick",
	}); err != nil {
		panic(err)
	}
	fmt.Println("craft chat sent")

	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			return
		default:
			pk, err := conn.ReadPacket()
			if err != nil {
				fmt.Println("read:", err)
				return
			}
			switch p := pk.(type) {
			case *packet.CommandOutput:
				fmt.Printf("command output: success=%d messages=%+v\n", p.SuccessCount, p.OutputMessages)
			case *packet.Text:
				fmt.Printf("chat: source=%q message=%q type=%d\n", p.SourceName, p.Message, p.TextType)
			}
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
