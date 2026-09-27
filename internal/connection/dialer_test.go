package connection

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/df-mc/go-nethernet/discovery"
	"github.com/sandertv/gophertunnel/minecraft"
)

func TestDecodeLANServerAcceptsNetherNetLANWorld(t *testing.T) {
	t.Parallel()
	want := discovery.ServerData{
		ServerName:            "Host",
		LevelName:             "Survival World",
		Protocol:              800,
		Version:               "1.26.51.01",
		GameType:              discovery.GameTypeSurvival,
		PlayerCount:           1,
		MaxPlayerCount:        8,
		AcceptsOnlineAuth:     true,
		AcceptsSelfSignedAuth: true,
		Nonce:                 "0123456789abcdef",
		ConnectionType:        lanConnectionType,
	}
	raw, err := want.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal server data: %v", err)
	}

	got, ok := decodeLANServer(raw, "survival world")
	if !ok {
		t.Fatal("decodeLANServer rejected a compatible LAN world")
	}
	if got.LevelName != want.LevelName {
		t.Fatalf("level name = %q, want %q", got.LevelName, want.LevelName)
	}
	if got.Nonce != want.Nonce {
		t.Fatalf("nonce = %q, want %q", got.Nonce, want.Nonce)
	}
	if got.Protocol != want.Protocol {
		t.Fatalf("protocol = %d, want %d", got.Protocol, want.Protocol)
	}
}

func TestDecodeLANServerRejectsEditorWorld(t *testing.T) {
	t.Parallel()
	data := discovery.ServerData{
		ServerName:     "Host",
		LevelName:      "Editor Project",
		ConnectionType: lanConnectionType,
		EditorWorld:    true,
	}
	raw, err := data.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal server data: %v", err)
	}

	if _, ok := decodeLANServer(raw, ""); ok {
		t.Fatal("decodeLANServer accepted an editor-mode world")
	}
}

func TestDecodeLANServerRejectsOtherConnectionType(t *testing.T) {
	t.Parallel()
	data := discovery.ServerData{
		ServerName:     "Host",
		LevelName:      "World",
		ConnectionType: 0,
	}
	raw, err := data.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal server data: %v", err)
	}

	if _, ok := decodeLANServer(raw, ""); ok {
		t.Fatal("decodeLANServer accepted a non-LAN connection type")
	}
}

func TestDecodeLANServerRejectsUnrelatedWorldName(t *testing.T) {
	t.Parallel()
	data := discovery.ServerData{
		ServerName:     "Host",
		LevelName:      "Creative World",
		ConnectionType: lanConnectionType,
	}
	raw, err := data.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal server data: %v", err)
	}

	if _, ok := decodeLANServer(raw, "survival world"); ok {
		t.Fatal("decodeLANServer accepted a world that does not match the filter")
	}
}

type recordingNetwork struct {
	address string
}

func (n *recordingNetwork) DialContext(_ context.Context, address string) (net.Conn, error) {
	n.address = address
	return nil, errors.New("recording network")
}

func (n *recordingNetwork) PingContext(context.Context, string) ([]byte, error) {
	return nil, errors.New("recording network")
}

func (n *recordingNetwork) Listen(string) (minecraft.NetworkListener, error) {
	return nil, errors.New("recording network")
}

func TestLANNetworkUsesDiscoveredNetworkID(t *testing.T) {
	t.Parallel()
	base := &recordingNetwork{}
	network := lanNetwork{Network: base, NetworkID: "12345"}
	_, err := network.DialContext(context.Background(), "192.168.1.114:7551")
	if err == nil {
		t.Fatal("DialContext should return the recording network error")
	}
	if base.address != "12345" {
		t.Fatalf("network address = %q, want discovered network ID %q", base.address, "12345")
	}
}

func TestServerAddressHostUsesConfiguredLANInterface(t *testing.T) {
	t.Parallel()
	got := serverAddressHost("lan", "192.168.1.114:0")
	if got != "192.168.1.114" {
		t.Fatalf("server address host = %q, want %q", got, "192.168.1.114")
	}
	if net.ParseIP(got) == nil {
		t.Fatalf("server address host is not an IP: %q", got)
	}
}
