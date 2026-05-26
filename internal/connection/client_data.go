package connection

import (
	"time"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

// mergeClientData takes skin-related fields and merges them with default device/game info
func mergeClientData(skinData login.ClientData) login.ClientData {
	skinData.ClientRandomID = int64(time.Now().UnixNano())
	skinData.CurrentInputMode = 1 // Keyboard & Mouse
	skinData.DefaultInputMode = 1
	skinData.DeviceModel = "Custom"
	skinData.DeviceOS = 7 // Windows 10/11
	skinData.DeviceID = login.DeviceID(uuid.New().String())
	skinData.GameVersion = protocol.CurrentVersion // Match current gophertunnel protocol version
	skinData.LanguageCode = "en_US"
	skinData.SelfSignedID = uuid.New().String()
	skinData.PlayFabID = "" // Leave empty, let server assign
	skinData.UIProfile = 0  // Classic UI
	
	// Ensure these are set
	skinData.TrustedSkin = true
	skinData.PremiumSkin = true
	skinData.OverrideSkin = true
	
	return skinData
}
