// Package player handles player, entity, and inventory-related packets.
package player

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"bedrock-ai/internal/bot"
)

// packetHandlers routes packet IDs to their handler functions.
var packetHandlers = map[uint32]func(*bot.Bot, packet.Packet) bool{
	packet.IDAddPlayer:                   handleAddPlayer,
	packet.IDMovePlayer:                  handleMovePlayerPacket,
	packet.IDCorrectPlayerMovePrediction: handleCorrectPredictionPacket,
	packet.IDRespawn:                     handleRespawn,
	packet.IDPlayerList:                  handlePlayerListPacket,
	packet.IDAddActor:                    handleAddActor,
	packet.IDAddItemActor:                handleAddItemActor,
	packet.IDCraftingData:                handleCraftingDataPacket,
	packet.IDMoveActorDelta:              handleMoveActorDelta,
	packet.IDMoveActorAbsolute:           handleMoveActorAbsolute,
	packet.IDSetActorMotion:              handleSetActorMotion,
	packet.IDTakeItemActor:               handleTakeItemActor,
	packet.IDRemoveActor:                 handleRemoveActor,
	packet.IDInventoryContent:            handleInventoryContent,
	packet.IDInventorySlot:               handleInventorySlot,
	packet.IDContainerOpen:               handleContainerOpen,
	packet.IDContainerClose:              handleContainerClose,
	packet.IDItemStackResponse:           handleItemStackResponse,
	packet.IDInventoryTransaction:        handleInventoryTransaction,
	packet.IDMobEquipment:                handleMobEquipment,
	packet.IDUpdateAttributes:            handleUpdateAttributesPacket,
	packet.IDNetworkStackLatency:         handleNetworkStackLatency,
	packet.IDPacketViolationWarning:      handlePacketViolationWarning,
	packet.IDCommandOutput:               handleCommandOutput,
	packet.IDAvailableCommands:           handleAvailableCommands,
	packet.IDSetTime:                     handleSetTime,
	packet.IDPlayStatus:                  handlePlayStatus,
	packet.IDLevelEvent:                  handleLevelEvent,
	packet.IDActorEvent:                  bot.HandleActorEvent,
	packet.IDSetActorData:                handleSetActorData,
	packet.IDUpdateTrade:                 handleUpdateTrade,
	packet.IDPlayerEnchantOptions:        handlePlayerEnchantOptions,
}

// HandlePlayerPacket dispatches a packet to the appropriate handler.
func HandlePlayerPacket(b *bot.Bot, pk packet.Packet) bool {
	if h, ok := packetHandlers[pk.ID()]; ok {
		return h(b, pk)
	}
	return false
}
