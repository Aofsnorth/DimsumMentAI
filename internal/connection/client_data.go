package connection

import (
	"encoding/hex"
	"strings"

	"bedrock-ai/internal/servercompat"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

// mergeClientData takes skin-related fields and merges them with default device/game info
func mergeClientData(skinData login.ClientData, profile servercompat.Profile) login.ClientData {
	dev := loadOrCreateDeviceIdentity()
	return mergeClientDataWithDevice(skinData, profile, dev)
}

func mergeClientDataWithDevice(skinData login.ClientData, profile servercompat.Profile, dev persistedDevice) login.ClientData {
	skinData.ClientRandomID = dev.ClientRandomID
	skinData.CurrentInputMode = 2 // Touch
	skinData.DefaultInputMode = 2
	skinData.DeviceModel = "SM-G973F"
	skinData.DeviceOS = 1 // Android
	skinData.DeviceID = login.DeviceID(dev.DeviceID)
	if profile.NetherGames {
		skinData.DeviceID = login.DeviceID(androidDeviceID(dev.DeviceID))
	}
	skinData.GameVersion = protocol.CurrentVersion // Match current gophertunnel protocol version
	skinData.LanguageCode = "en_US"
	skinData.SelfSignedID = dev.SelfSignedID
	skinData.PlayFabID = "" // Let gophertunnel fill a valid login value.
	skinData.UIProfile = 0  // Classic UI

	// Ensure these are set
	skinData.TrustedSkin = true
	skinData.PremiumSkin = true
	skinData.OverrideSkin = true

	return skinData
}

func androidDeviceID(id string) string {
	id = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
	if len(id) == 32 {
		if _, err := hex.DecodeString(id); err == nil {
			return id
		}
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}
