package connection

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

var deviceIdentityPath = "data/device_identity.json"

type persistedDevice struct {
	ClientRandomID int64  `json:"client_random_id"`
	SelfSignedID   string `json:"self_signed_id"`
	DeviceID       string `json:"device_id"`
}

func loadOrCreateDeviceIdentity() persistedDevice {
	data, err := os.ReadFile(deviceIdentityPath)
	if err == nil {
		var dev persistedDevice
		if json.Unmarshal(data, &dev) == nil && dev.SelfSignedID != "" && dev.DeviceID != "" && dev.ClientRandomID != 0 {
			return dev
		}
	}
	dev := persistedDevice{
		ClientRandomID: int64(uuid.New().ID()),
		SelfSignedID:   uuid.New().String(),
		DeviceID:       uuid.New().String(),
	}
	_ = os.MkdirAll(filepath.Dir(deviceIdentityPath), 0755)
	if encoded, err := json.Marshal(dev); err == nil {
		_ = os.WriteFile(deviceIdentityPath, encoded, 0600)
	}
	return dev
}
