package event

import (
	"github.com/sandertv/gophertunnel/minecraft"
)

type SpawnEvent struct {
	GameData minecraft.GameData
}

type ChatEvent struct {
	Message    string
	SourceName string
	TextType   byte
}

type DisconnectEvent struct {
	Reason string
}
