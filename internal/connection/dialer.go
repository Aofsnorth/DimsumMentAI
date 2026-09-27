package connection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/config"
	"bedrock-ai/internal/servercompat"

	"github.com/df-mc/go-nethernet"
	"github.com/df-mc/go-nethernet/discovery"
	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"golang.org/x/oauth2"
)

// lanConnectionType is the ConnectionType a host advertises for a LAN world
// signalled over NetherNet.
const lanConnectionType = 4

type Dialer struct {
	mu           sync.RWMutex
	cfg          config.ServerConfig
	identityData login.IdentityData
	clientData   login.ClientData
}

func NewDialer(cfg config.ServerConfig, identityData login.IdentityData, clientData login.ClientData) *Dialer {
	return &Dialer{cfg: cfg, identityData: identityData, clientData: clientData}
}

// snapshot returns a copy of the current server config.
//
// Dial takes a snapshot rather than reading fields directly because the target
// can be changed while a connection is up (the join action), and a half-applied
// config would dial one host while detecting the compatibility profile of
// another.
func (d *Dialer) snapshot() config.ServerConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg
}

// SetTarget points every future Dial at a different server.
//
// Host detection for the compatibility profile follows the new host, and LAN
// discovery is switched off: the player named an explicit address, so scanning
// for a nearby world would only risk joining the wrong one.
func (d *Dialer) SetTarget(address string) error {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return fmt.Errorf("address %q must be host:port: %w", address, err)
	}
	host = strings.TrimSpace(host)
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("address %q has an invalid port", address)
	}
	if host == "" {
		return fmt.Errorf("address %q has no host", address)
	}

	disabled := false
	d.mu.Lock()
	d.cfg.Host = host
	d.cfg.Port = port
	d.cfg.LANDiscovery = &disabled
	d.mu.Unlock()
	return nil
}

// Target reports the address the dialer will use.
func (d *Dialer) Target() string {
	return d.snapshot().Address()
}

func (d *Dialer) Dial() (*minecraft.Conn, error) {
	cfg := d.snapshot()
	dialer := d.newMinecraftDialer(cfg)

	if !cfg.Offline {
		tokenPath := "configs/token.json"
		token, err := loadToken(tokenPath)
		if err != nil {
			fmt.Println("No persistent Microsoft Live token found. Starting interactive Microsoft login flow...")
			token, err = auth.RequestLiveToken()
			if err != nil {
				return nil, fmt.Errorf("microsoft oauth login: %w", err)
			}
			if err := saveToken(tokenPath, token); err != nil {
				fmt.Printf("Warning: failed to save token: %v\n", err)
			} else {
				fmt.Printf("Successfully saved Microsoft Live token to %s\n", tokenPath)
			}
		} else {
			fmt.Printf("Loaded persistent Microsoft Live token from %s\n", tokenPath)
		}
		dialer.TokenSource = auth.RefreshTokenSource(token)
	}

	// Diagnostic override: BOT_DIAL_ADDR lets us route the bot through the local
	// MITM proxy (cmd/proxy) while servercompat detection still keys off the
	// configured Host. Unset in normal operation.
	dialAddr := cfg.Address()
	if override := os.Getenv("BOT_DIAL_ADDR"); override != "" {
		fmt.Printf("BOT_DIAL_ADDR set: dialing %s instead of %s (compat still keyed on host %q)\n", override, dialAddr, cfg.Host)
		dialAddr = override
	}

	// Diagnostic opt-in: record every packet in both directions to a JSONL file.
	// Kept on the dial path (not newMinecraftDialer) so the capture can learn the
	// remote address from the live connection after Dial returns.
	var capture *packetCapture
	if capturePath := os.Getenv("BOT_PACKET_CAPTURE"); capturePath != "" {
		if c, err := newPacketCapture(capturePath); err != nil {
			fmt.Printf("BOT_PACKET_CAPTURE: cannot open %s: %v\n", capturePath, err)
		} else {
			capture = c
			dialer.PacketFunc = c.record
			fmt.Printf("BOT_PACKET_CAPTURE: recording to %s\n", capturePath)
		}
	}

	if cfg.LANDiscoveryEnabled() && os.Getenv("BOT_DIAL_ADDR") == "" {
		if conn, err := d.dialLAN(cfg, dialer); err == nil {
			return conn, nil
		} else {
			slog.Warn("LAN discovery failed; falling back to RakNet", "error", err)
		}
	}

	conn, err := dialer.Dial("raknet", dialAddr)
	if err != nil {
		// A server whose texture packs carry behaviour modules can never pass
		// gophertunnel's ResourcePackStack check: the handler hardcodes
		// hasPack(uuid, version, hasBehaviours=false), so a pack reporting
		// true is rejected as "not downloaded" however completely it was
		// fetched. No amount of retrying downloads fixes that, but refusing
		// the pack does — the pack is then recorded as deliberately ignored,
		// which is the other thing hasPack accepts.
		//
		// Falling back here rather than making the user read the docs: the
		// symptom is a dial failure that looks like a network problem and is
		// not one.
		if cfg.ResourcePacks == config.ResourcePacksDownload && isPackNotDownloaded(err) {
			slog.Warn("server's texture packs cannot satisfy the protocol check; retrying without them",
				"host", cfg.Host,
				"hint", "set server.resource_packs to \"skip\" to skip this retry on future runs",
			)
			fallback := d.newMinecraftDialer(withResourcePacks(cfg, config.ResourcePacksSkip))
			if !cfg.Offline {
				if token, tokErr := loadToken("configs/token.json"); tokErr == nil {
					fallback.TokenSource = auth.RefreshTokenSource(token)
				}
			}
			conn, err = fallback.Dial("raknet", dialAddr)
		}
		if err != nil {
			return nil, fmt.Errorf("dial server %s: %w", dialAddr, err)
		}
	}

	if capture != nil {
		capture.setRemote(conn.RemoteAddr())
	}

	return conn, nil
}

// isPackNotDownloaded reports whether a dial failed specifically on the resource
// pack check. Matching on the message is unpleasant, but the error is produced
// inside gophertunnel's packet handler and carries no typed marker to match on.
func isPackNotDownloaded(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not downloaded")
}

// withResourcePacks returns a copy of cfg with the resource pack mode replaced.
func withResourcePacks(cfg config.ServerConfig, mode string) config.ServerConfig {
	cfg.ResourcePacks = mode
	return cfg
}

// lanDiscoveryTimeout bounds how long the bot waits for a LAN world to answer
// discovery. NetherNet ICE/DTLS negotiation after discovery needs its own, much
// longer budget, so the two phases use separate contexts.
const (
	lanDiscoveryTimeout = 5 * time.Second
	lanDialTimeout      = 60 * time.Second
)

func (d *Dialer) dialLAN(cfg config.ServerConfig, dialer minecraft.Dialer) (*minecraft.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lanDiscoveryTimeout)
	defer cancel()

	listenAddress, broadcastAddress := lanDiscoveryEndpoint(cfg.Host)
	listener, err := (discovery.ListenConfig{BroadcastAddress: broadcastAddress}).Listen(listenAddress)
	if err != nil {
		return nil, fmt.Errorf("listen for LAN discovery on %s: %w", listenAddress, err)
	}
	slog.Debug("scanning for LAN worlds",
		slog.String("listen", listenAddress),
		slog.String("broadcast", broadcastAddress.String()),
		slog.String("world_filter", cfg.LANWorld),
	)
	closeListener := true
	defer func() {
		if closeListener {
			_ = listener.Close()
		}
	}()

	var lastDialErr error
	triedNetworkIDs := make(map[uint64]struct{})
	for {
		for networkID, raw := range listener.Responses() {
			if _, tried := triedNetworkIDs[networkID]; tried {
				continue
			}
			triedNetworkIDs[networkID] = struct{}{}
			server, ok := decodeLANServer(raw, cfg.LANWorld)
			if !ok {
				continue
			}

			address := strconv.FormatUint(networkID, 10)
			loginAddress := net.JoinHostPort(serverAddressHost(cfg.Host, listenAddress), strconv.Itoa(cfg.Port))
			slog.Debug("dialing discovered LAN world",
				slog.String("level", server.LevelName),
				slog.String("host", server.ServerName),
				slog.String("game_version", server.Version),
				slog.String("network_id", address),
			)
			network := minecraft.NetherNet{
				Signaling: listener,
				Dialer: nethernet.Dialer{
					AllowIdentitylessServer: cfg.Offline,
				},
			}
			// The host issues a Nonce with its advertisement and rejects clients
			// that do not echo it back in the Login ClientData.
			lanDialer := dialer
			lanDialer.ClientData.Nonce = server.Nonce
			// The dial must NOT derive from the discovery context: that context is
			// cancelled as soon as the scan window ends, which would abort a
			// negotiation that is still legitimately in progress.
			dialCtx, dialCancel := context.WithTimeout(context.Background(), lanDialTimeout)
			conn, err := lanDialer.DialContextNetwork(dialCtx, lanNetwork{
				Network:   network,
				NetworkID: address,
			}, loginAddress)
			dialCancel()
			if err != nil {
				lastDialErr = fmt.Errorf("dial discovered LAN world %q (%s): %w", server.LevelName, address, err)
				continue
			}

			closeListener = false
			go func() {
				<-conn.Context().Done()
				_ = listener.Close()
			}()
			return conn, nil
		}

		select {
		case <-ctx.Done():
			if lastDialErr != nil {
				return nil, lastDialErr
			}
			return nil, fmt.Errorf("no compatible NetherNet LAN world discovered via %s to %s", listenAddress, broadcastAddress)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type lanNetwork struct {
	minecraft.Network
	NetworkID string
}

func (n lanNetwork) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	return n.Network.DialContext(ctx, n.NetworkID)
}

func (n lanNetwork) PingContext(context.Context, string) ([]byte, error) {
	return nil, errors.New("LAN NetherNet does not support ping")
}

func serverAddressHost(configuredHost, listenAddress string) string {
	configuredHost = strings.TrimSpace(configuredHost)
	if configuredHost != "" && !strings.EqualFold(configuredHost, "lan") {
		return configuredHost
	}
	if host, _, err := net.SplitHostPort(listenAddress); err == nil && host != "0.0.0.0" {
		return host
	}
	return "127.0.0.1"
}

// decodeLANServer decodes the world advertisement and filters it down to worlds
// this bot can actually join over NetherNet.
func decodeLANServer(raw []byte, worldName string) (discovery.ServerData, bool) {
	var server discovery.ServerData
	if err := server.UnmarshalBinary(raw); err != nil {
		return discovery.ServerData{}, false
	}
	// ConnectionType 4 marks a LAN world signalled over NetherNet. Editor-mode
	// projects are only visible to clients in Editor Mode.
	if server.ConnectionType != lanConnectionType || server.EditorWorld {
		return discovery.ServerData{}, false
	}
	if worldName == "" {
		return server, true
	}
	worldName = strings.TrimSpace(worldName)
	return server, strings.EqualFold(server.LevelName, worldName) || strings.EqualFold(server.ServerName, worldName)
}

func lanDiscoveryEndpoint(preferredHost string) (string, *net.UDPAddr) {
	preferred := net.ParseIP(strings.TrimSpace(preferredHost))
	if preferred != nil {
		preferred = preferred.To4()
	}

	for _, iface := range usableInterfaces() {
		for _, addr := range interfaceIPv4Addrs(iface) {
			if preferred != nil && !preferred.Equal(addr.ip) {
				continue
			}
			return net.JoinHostPort(addr.ip.String(), "0"), &net.UDPAddr{IP: addr.broadcast, Port: discovery.DefaultPort}
		}
	}

	return "0.0.0.0:0", &net.UDPAddr{IP: net.IPv4bcast, Port: discovery.DefaultPort}
}

type interfaceIPv4Addr struct {
	ip        net.IP
	broadcast net.IP
}

func usableInterfaces() []net.Interface {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := make([]net.Interface, 0, len(interfaces))
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			result = append(result, iface)
		}
	}
	return result
}

func interfaceIPv4Addrs(iface net.Interface) []interfaceIPv4Addr {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	result := make([]interfaceIPv4Addr, 0, len(addrs))
	for _, addr := range addrs {
		ip, network, err := net.ParseCIDR(addr.String())
		if err != nil || ip.To4() == nil {
			continue
		}
		ip = ip.To4()
		mask := network.Mask
		broadcast := make(net.IP, net.IPv4len)
		for i := range broadcast {
			broadcast[i] = ip[i] | ^mask[i]
		}
		result = append(result, interfaceIPv4Addr{ip: ip, broadcast: broadcast})
	}
	return result
}

// refuseResourcePacks tells gophertunnel to skip every resource pack.
//
// The bot is headless: it never renders a texture, so a pack is pure download
// cost. But gophertunnel does not merely skip it — a pack the connection never
// downloaded fails the "is every pack on the stack present?" check in
// handleResourcePackStack, and the whole login dies with
// "texture pack (UUID=…, version=…) not downloaded". That is exactly what
// play.nexusone.fun sends, and it is why the bot could not get into the world
// at all.
//
// Returning false does two useful things: the pack is not fetched, and
// gophertunnel records it as deliberately ignored, which is one of the two
// things hasPack accepts. So the stack check passes and the world loads.
func refuseResourcePacks(id uuid.UUID, version string, current, total int) bool {
	slog.Debug("refusing resource pack (bot is headless)", "uuid", id, "version", version,
		"current", current, "total", total)
	return false
}

// acceptResourcePacks downloads every pack and caches it on disk, for servers
// that will not admit a client which refused one.
func acceptResourcePacks(id uuid.UUID, version string, current, total int) bool {
	slog.Info("downloading resource pack", "uuid", id, "version", version,
		"current", current, "total", total)
	return true
}

func (d *Dialer) newMinecraftDialer(cfg config.ServerConfig) minecraft.Dialer {
	profile := servercompat.Detect(cfg.Host)

	md := minecraft.Dialer{
		IdentityData: d.identityData,
		ClientData:   mergeClientData(d.clientData, profile),
	}
	switch cfg.ResourcePacks {
	case config.ResourcePacksDownload:
		md.DownloadResourcePack = acceptResourcePacks
		md.ResourcePackCache = minecraft.DirResourcePackCache{Dir: cfg.ResourcePackDir}
	default:
		md.DownloadResourcePack = refuseResourcePacks
	}
	return md
}

func loadToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func saveToken(path string, token *oauth2.Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
