package connection

import (
	"time"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

// mergeClientData takes skin-related fields and merges them with default device/game info
func mergeClientData(skinData login.ClientData) login.ClientData {
	skinData.ClientRandomID = int64(time.Now().UnixNano())
	skinData.CurrentInputMode = 2 // touch / generic
	skinData.DefaultInputMode = 2
	skinData.DeviceModel = "Bedrock AI Bot"
	skinData.DeviceOS = 1 // Android/Generic
	skinData.DeviceID = login.DeviceID(uuid.New().String())
	skinData.GameVersion = "1.26.20" // Important for compatibility
	skinData.LanguageCode = "en_US"
	skinData.SelfSignedID = uuid.New().String()
	skinData.PlayFabID = "50afc685b094b0f3" // generic mock ID
	skinData.UIProfile = 0
	
	// Ensure these are set
	skinData.TrustedSkin = true
	skinData.PremiumSkin = true
	skinData.OverrideSkin = true
	
	return skinData
}
