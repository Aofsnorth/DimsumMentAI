// Command skinprobe bisects why the target server rejects the bot's login with
// disconnectionScreen.invalidSkin.
//
// The bot's skin pipeline was accepted by Geyser front-ends and by a
// client-hosted LAN world, but this vanilla 1.26.50 host refuses the same login
// chain. The probe replays the login the bot sends and changes ONE dimension per
// attempt — the authentication format first (offline OIDC vs legacy self-signed
// vs authenticated Mojang token), then individual skin fields — so the failing
// piece is found by observation instead of guesswork:
//
//	go run ./cmd/skinprobe [-config configs/bot.yaml] [-only baseline,geom128]
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"bedrock-ai/internal/config"
	"bedrock-ai/internal/skin"

	"github.com/google/uuid"
	"github.com/sandertv/go-raknet"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"golang.org/x/oauth2"
)

// auth modes. offline is what the bot sends today; legacy is the pre-1.21.90
// self-signed chain; online authenticates with the saved Microsoft token (the
// path the bot used in earlier successful sessions).
const (
	authOffline = "oidc"
	authLegacy  = "legacy"
	authOnline  = "online"
)

func main() {
	configPath := flag.String("config", "configs/bot.yaml", "bot config with server + skin settings")
	only := flag.String("only", "", "comma-separated subset of variants to test")
	spawnWait := flag.Duration("wait", 3*time.Second, "how long to hold a successful login")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	provider := skin.NewProvider(cfg.Skin)
	assets, err := provider.Provide()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build skin assets: %v\n", err)
		os.Exit(1)
	}

	identity := login.IdentityData{
		Identity:    uuid.New().String(),
		DisplayName: cfg.Bot.Name,
	}

	// Step 0: what version is the server actually running? A protocol newer
	// than gophertunnel's means the server expects a login chain we cannot
	// produce, and no amount of field tweaking fixes that.
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	addr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
	if pong, err := raknet.PingContext(pingCtx, addr); err != nil {
		fmt.Printf("ping %s: %v (continuing with variants)\n", addr, err)
	} else if mcpeVersion, mcpeProtocol, ok := parseMCPEPong(pong); !ok {
		fmt.Printf("pong from %s was not an MCPE status line: %q\n", addr, pong)
	} else {
		fmt.Printf("server: protocol=%d version=%q\n", mcpeProtocol, mcpeVersion)
		fmt.Printf("gophertunnel speaks: protocol=%d version=%q\n",
			protocol.CurrentProtocol, protocol.CurrentVersion)
		if mcpeProtocol != protocol.CurrentProtocol {
			fmt.Printf(">>> PROTOCOL MISMATCH: server=%d ours=%d — the login chain is built for the wrong version.\n",
				mcpeProtocol, protocol.CurrentProtocol)
		}
	}

	image, err := skin.LoadImage(cfg.Skin.ImagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load skin image: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("skin image: %dx%d, embedded geometry texture dims: 64x64\n\n", image.Width, image.Height)

	// Auth first (the format changed in 1.26.10 and offline servers differ in
	// what they tolerate), then the skin fields under the default auth mode.
	variants := []struct {
		name string
		auth string
		mod  func(cd login.ClientData) login.ClientData
	}{
		{authOffline + ":baseline", authOffline, func(cd login.ClientData) login.ClientData { return cd }},
		{authOffline + ":geom-min", authOffline, geomMin},
		{authOffline + ":geom-fmt116", authOffline, geomFmt116},
		{authOffline + ":arm-wide", authOffline, armWide},
		{authOffline + ":nogeom", authOffline, func(cd login.ClientData) login.ClientData {
			cd.SkinGeometry = ""
			return cd
		}},
		{authOffline + ":skinid-plain", authOffline, func(cd login.ClientData) login.ClientData {
			cd.SkinID = uuid.NewString()
			return cd
		}},
		{authOnline + ":baseline", authOnline, func(cd login.ClientData) login.ClientData { return cd }},
		{authOnline + ":geom-min", authOnline, geomMin},
		{authOnline + ":geom-fmt116", authOnline, geomFmt116},
		{authLegacy + ":baseline", authLegacy, func(cd login.ClientData) login.ClientData { return cd }},
	}

	skip := map[string]bool{}
	for _, name := range strings.Split(*only, ",") {
		if name = strings.TrimSpace(name); name != "" {
			skip[name] = false
		}
	}
	testAll := len(skip) == 0

	tokenSource := loadTokenSource("configs/token.json")

	passed := ""
	for _, v := range variants {
		if !testAll {
			if _, want := skip[v.name]; !want {
				continue
			}
		}
		fmt.Printf("--- variant %s ---\n", v.name)
		if dialVariant(cfg.Server, identity, v.mod(assets.ClientData), v.auth, tokenSource, *spawnWait) {
			passed = v.name
			break
		}
	}

	fmt.Println()
	if passed == "" {
		fmt.Println("RESULT: every variant was rejected — the failing piece is not among them.")
		fmt.Println("Next step: capture the real client's login (cmd/proxy) and diff the skin chain.")
		os.Exit(1)
	}
	fmt.Printf("RESULT: variant %q passed login.\n", passed)
}

// armWide switches to the wide (Steve) model consistently: patch references
// geometry.humanoid.custom and ArmSize is wide.
func armWide(cd login.ClientData) login.ClientData {
	patch, err := json.Marshal(map[string]any{"geometry": map[string]any{"default": "geometry.humanoid.custom"}})
	if err != nil {
		panic(err)
	}
	cd.SkinResourcePatch = b64(patch)
	cd.ArmSize = "wide"
	return cd
}

// geomMin strips the geometry JSON down to only the customSlim definition. The
// bundled cape + wide-custom geometries are things a real client never sends.
func geomMin(cd login.ClientData) login.ClientData {
	cd.SkinGeometry = b64(geomFilter(skin.DefaultGeometry, "geometry.humanoid.customSlim"))
	return cd
}

// geomFmt116 rewrites the geometry format_version to a newer one in case the
// host rejects the 1.12.0 file.
func geomFmt116(cd login.ClientData) login.ClientData {
	cd.SkinGeometry = b64(geomFormatVersion(skin.DefaultGeometry, "1.16.0"))
	return cd
}

// dialVariant performs one login with the given client data and auth mode and
// reports whether the server accepted it. Device fields replicate what the bot
// sends (the same persisted identity is reused so the fingerprint matches).
func dialVariant(cfg config.ServerConfig, identity login.IdentityData, cd login.ClientData, mode string, tokenSource oauth2.TokenSource, wait time.Duration) bool {
	applyDeviceFields(&cd)

	md := minecraft.Dialer{
		IdentityData: identity,
		ClientData:   cd,
		DownloadResourcePack: func(id uuid.UUID, version string, current, total int) bool {
			return false
		},
	}
	switch mode {
	case authLegacy:
		md.EnableLegacyAuth = true
	case authOnline:
		if tokenSource == nil {
			fmt.Printf("  FAIL: no Microsoft token available (configs/token.json missing)\n")
			return false
		}
		md.TokenSource = tokenSource
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := md.DialContext(ctx, "raknet", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return false
	}
	defer func() { _ = conn.Close() }()

	time.Sleep(wait)
	fmt.Printf("  PASS: login accepted and held for %v\n", wait)
	return true
}

// loadTokenSource returns a refresh token source for the saved Microsoft token,
// or nil when the file is absent/invalid (the online variants then report a
// clear failure instead of prompting for an interactive login).
func loadTokenSource(path string) oauth2.TokenSource {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var tok oauth2.Token
	if err := json.Unmarshal(raw, &tok); err != nil || tok.AccessToken == "" {
		return nil
	}
	fmt.Printf("loaded Microsoft Live token from %s\n", path)
	return auth.RefreshTokenSource(&tok)
}

// applyDeviceFields mirrors connection.mergeClientDataWithDevice so the probe's
// login is indistinguishable from the bot's apart from the field under test.
func applyDeviceFields(cd *login.ClientData) {
	dev := loadDevice()
	cd.ClientRandomID = dev.ClientRandomID
	cd.CurrentInputMode = 2
	cd.DefaultInputMode = 2
	cd.DeviceModel = "SM-G973F"
	cd.DeviceOS = 1
	cd.DeviceID = login.DeviceID(dev.DeviceID)
	cd.GameVersion = protocol.CurrentVersion
	cd.LanguageCode = "en_US"
	cd.SelfSignedID = dev.SelfSignedID
	cd.PlayFabID = ""
	cd.UIProfile = 0
}

type device struct {
	ClientRandomID int64  `json:"client_random_id"`
	SelfSignedID   string `json:"self_signed_id"`
	DeviceID       string `json:"device_id"`
}

func loadDevice() device {
	raw, err := os.ReadFile("data/device_identity.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read data/device_identity.json: %v (run the bot once first)\n", err)
		os.Exit(1)
	}
	var dev device
	if err := json.Unmarshal(raw, &dev); err != nil {
		fmt.Fprintf(os.Stderr, "decode data/device_identity.json: %v\n", err)
		os.Exit(1)
	}
	return dev
}

// geomFilter keeps only the geometry whose identifier matches, mirroring what a
// real client sends for a single-model custom skin.
func geomFilter(geometry []byte, identifier string) []byte {
	doc, geoms := parseGeometries(geometry)
	kept := make([]any, 0, 1)
	for _, g := range geoms {
		if idOf(g) == identifier {
			kept = append(kept, g)
		}
	}
	doc["minecraft:geometry"] = kept
	return reencode(doc)
}

// geomFormatVersion rewrites the geometry document's format_version.
func geomFormatVersion(geometry []byte, version string) []byte {
	doc, _ := parseGeometries(geometry)
	doc["format_version"] = version
	return reencode(doc)
}

func parseGeometries(geometry []byte) (map[string]any, []any) {
	var doc map[string]any
	if err := json.Unmarshal(geometry, &doc); err != nil {
		panic("embedded geometry.json is not valid JSON: " + err.Error())
	}
	geoms, _ := doc["minecraft:geometry"].([]any)
	return doc, geoms
}

func idOf(g any) string {
	obj, ok := g.(map[string]any)
	if !ok {
		return ""
	}
	desc, ok := obj["description"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := desc["identifier"].(string)
	return id
}

func reencode(doc map[string]any) []byte {
	out, err := json.Marshal(doc)
	if err != nil {
		panic("re-encode geometry: " + err.Error())
	}
	return out
}

func b64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// parseMCPEPong extracts the game version and protocol number from a RakNet
// pong payload. The MCPE status line is
// "MCPE;<motd>;<protocol>;<version>;<players>;<max>;...".
func parseMCPEPong(pong []byte) (version string, protocolNum int, ok bool) {
	idx := bytes.Index(pong, []byte("MCPE;"))
	if idx < 0 {
		return "", 0, false
	}
	parts := strings.Split(string(pong[idx:]), ";")
	if len(parts) < 4 {
		return "", 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return "", 0, false
	}
	return strings.TrimSpace(parts[3]), n, true
}
