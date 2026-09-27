// Command lanprobe is a diagnostic that answers one question: is a Bedrock
// single-player world actually advertising itself over NetherNet on this
// machine, or is the bot failing to find one that is there?
//
// It sends a real discovery.RequestPacket from a single unbound socket to
// several targets, most importantly by unicast to 127.0.0.1:7551. A world that
// is open to LAN always answers on loopback, so a silent loopback probe is
// conclusive: the world is not opened to LAN and no bot code change can help.
//
// It also pings the RakNet listener (unicast to the host and broadcast to the
// subnet) so a machine whose broadcasts are filtered by the network can be
// told apart from a world that is not open to LAN at all.
//
// Usage:
//
//	go run ./cmd/lanprobe [-w 5s] [-host 192.168.1.19] [-raknet-port 19132]
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/df-mc/go-nethernet/discovery"
	"github.com/sandertv/go-raknet"
)

func main() {
	window := flag.Duration("w", 5*time.Second, "how long to wait for responses")
	host := flag.String("host", "", "LAN host to probe directly (NetherNet unicast + RakNet unicast)")
	raknetPort := flag.Int("raknet-port", 19132, "RakNet port the host listens on")
	broadcast := flag.String("broadcast", "192.168.1.255", "subnet broadcast address for discovery + RakNet ping")
	flag.Parse()

	socket, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = socket.Close() }()
	fmt.Printf("probe socket: %s\n", socket.LocalAddr())

	targets := []string{
		"127.0.0.1:7551", // decisive: loopback unicast, no broadcast involved
	}
	if *host != "" {
		targets = append(targets, net.JoinHostPort(*host, "7551"))
	}
	targets = append(targets, net.JoinHostPort(*broadcast, "7551"))
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

	pingRakNet(net.JoinHostPort(*host, strconv.Itoa(*raknetPort)))

	// RakNet unicast to the host: answers when the world is open to LAN, and
	// also when a dedicated server runs there, so it narrows the problem but
	// never proves it on its own.
	if *host != "" {
		pingRakNet(net.JoinHostPort(*host, strconv.Itoa(*raknetPort)))
	}
	// RakNet broadcast: this is how the Friends tab finds LAN worlds. A host
	// that answers unicast but not broadcast sits behind filtered broadcasts.
	pingRakNet(net.JoinHostPort(*broadcast, strconv.Itoa(*raknetPort)))
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
		fmt.Println("\nRESULT: no world answered NetherNet discovery.")
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

func pingRakNet(addr string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pong, err := raknet.PingContext(ctx, addr)
	if err != nil {
		fmt.Printf("raknet ping %s: %v\n", addr, err)
		return
	}
	fmt.Printf("raknet ping %s: %s\n", addr, summarizePong(pong))
}

func summarizePong(pong []byte) string {
	idx := bytes.Index(pong, []byte("MCPE;"))
	if idx < 0 {
		return strconv.Quote(string(pong))
	}
	parts := strings.SplitN(string(pong[idx:]), ";", 8)
	if len(parts) < 4 {
		return strconv.Quote(string(pong))
	}
	return fmt.Sprintf("motd=%q protocol=%s version=%q players=%q", parts[1], parts[2], parts[3], parts[4])
}
