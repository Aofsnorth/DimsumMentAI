package main

import (
	"fmt"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func main() {
	fmt.Printf("GameType=%+v\n", &packet.UpdatePlayerGameType{})
}
