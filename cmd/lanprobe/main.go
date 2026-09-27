// Command lanprobe is a diagnostic that answers one question: is a Bedrock
// single-player world actually advertising itself over NetherNet on this
// machine, or is the bot failing to find one that is there?
//
// It sends a real discovery.RequestPacket from a single unbound socket to
// several targets, most importantly by unicast to 127.0.0.1:7551. A world that
// is open to LAN always answers on loopback, so a silent loopback probe is
// conclusive: the world is not opened to LAN and no bot code change can help.
//
// Usage:
//
//	go run ./cmd/lanprobe [-w 5s]
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/df-mc/go-nethernet/discovery"
)

func main() {
	window := flag.Duration("w", 5*time.Second, "how long to wait for responses")
	flag.Parse()

	socket, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = socket.Close() }()
	fmt.Printf("probe socket: %s\n", socket.LocalAddr())

	targets := []string{
		"127.0.0.1:7551",     // decisive: loopback unicast, no broadcast involved
		"192.168.1.255:7551", // subnet broadcast, what the bot uses
	}
	for _, target := range targets {
		addr, err := net.ResolveUDPAddr("udp4", target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve %s: %v\n", target, err)
			continue
		}
		payload := discovery.Marshal(&discovery.RequestPacket{}, rand.Uint64())
		if _, err := socket.WriteTo(payload, addr); err != nil {
			fmt.Fprintf(os.Stderr, "send to %s: %v\n", target, err)
			continue
		}
		fmt.Printf("sent RequestPacket to %s (%d bytes)\n", target, len(payload))
	}

	responses := make(map[uint64][]byte)
	deadline := time.Now().Add(*window)
	buf := make([]byte, 65535)
	for time.Now().Before(deadline) {
		_ = socket.SetReadDeadline(deadline)
		n, addr, err := socket.ReadFrom(buf)
		if err != nil {
			break
		}
		packet, senderID, err := discovery.Unmarshal(buf[:n])
		if err != nil {
			fmt.Printf("undecodable %d bytes from %s: %v\n", n, addr, err)
			continue
		}
		response, ok := packet.(*discovery.ResponsePacket)
		if !ok {
			fmt.Printf("non-response %T from %s (network %d)\n", packet, addr, senderID)
			continue
		}
		responses[senderID] = response.ApplicationData
		fmt.Printf("ResponsePacket from %s: network_id=%d payload=%d bytes\n", addr, senderID, len(response.ApplicationData))
	}

	if len(responses) == 0 {
		fmt.Println("\nRESULT: no world answered. The world is NOT open to LAN.")
		os.Exit(1)
	}
	fmt.Printf("\nRESULT: %d world(s) advertised:\n", len(responses))
	for networkID, raw := range responses {
		var server discovery.ServerData
		if err := server.UnmarshalBinary(raw); err != nil {
			fmt.Printf("  network %d: ServerData decode failed: %v\n", networkID, err)
			continue
		}
		fmt.Printf("  network %d: level=%q host=%q game=%d players=%d/%d conn_type=%d editor=%v version=%q\n",
			networkID, server.LevelName, server.ServerName, server.GameType,
			server.PlayerCount, server.MaxPlayerCount, server.ConnectionType, server.EditorWorld, server.Version)
		fmt.Printf("             nonce=%q protocol=%q accepts_online_auth=%v\n",
			server.Nonce, server.Protocol, server.AcceptsOnlineAuth)
	}
}
