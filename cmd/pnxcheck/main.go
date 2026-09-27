// Command pnxcheck replays the exact login payload the bot builds and runs it
// through a local replica of PowerNukkitX's login validation chain, so the
// disconnectionScreen.invalidSkin rejection can be diagnosed without the
// server: the first check PNX would fail is printed with the offending value.
//
// PNX (Anvil-MC hosts run it) validates, in order, in LoginHandler:
//
//  1. login packet auth payload (auth type byte -> PlayerAuthenticationType,
//     offset +1 per LoginSerializer_v818)
//
//  2. identity token (SELF_SIGNED -> OFFLINE_CONSUMER, requires exp claim)
//
//  3. identity claims: identity public key read from the token's "cpk" claim
//
//  4. client JWT signature against that key (jose4j, ES384)
//
//  5. ClientChainData.from: every listed claim must exist with an exact
//     Java type; enum claims are resolved with from(), which throws (-> null
//     -> INVALID_PLATFORM_SKIN) when the ordinal is out of range
//
//  6. ClientSkinData.readSkin: strict per-field type checks, catch-all -> null
//
//  7. SkinUtils.isValid: SkinId non-empty <100, image >=32x32 and
//     >= SINGLE_SKIN_SIZE bytes, string fields < limits, resource patch JSON
//
//     go run ./cmd/pnxcheck [-config configs/bot.yaml]
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	"bedrock-ai/internal/config"
	"bedrock-ai/internal/skin"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

var failures int

func main() {
	configPath := flag.String("config", "configs/bot.yaml", "bot config with skin settings")
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

	cd := assets.ClientData
	applyDeviceFields(&cd)

	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate key: %v\n", err)
		os.Exit(1)
	}

	request := login.EncodeOffline(identity, cd, key, false)

	// Dissect the connection request the same way the server's login
	// serializer does: int32 length + JSON, int32 length + client JWT.
	r := bytes.NewReader(request)
	var chainLen, rawLen int32
	_ = binary.Read(r, binary.LittleEndian, &chainLen)
	chainJSON := make([]byte, chainLen)
	_, _ = r.Read(chainJSON)
	_ = binary.Read(r, binary.LittleEndian, &rawLen)
	rawToken := make([]byte, rawLen)
	_, _ = r.Read(rawToken)

	var req struct {
		Certificate        string `json:"Certificate"`
		AuthenticationType int    `json:"AuthenticationType"`
		Token              string `json:"Token"`
	}
	if err := json.Unmarshal(chainJSON, &req); err != nil {
		fmt.Printf("FATAL connection request JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("connection request: AuthenticationType=%d (server maps byte+1 -> PlayerAuthenticationType) Token=%d bytes Chain=%q\n\n",
		req.AuthenticationType, len(req.Token), req.Certificate)

	// PNX LoginHandler: type UNKNOWN -> NOT_AUTHENTICATED. The serializer
	// maps byte n to values()[n+1]: 0=FULL 1=GUEST 2=SELF_SIGNED.
	authType := req.AuthenticationType + 1 // UNKNOWN=0 FULL=1 GUEST=2 SELF_SIGNED=3
	check("auth type resolves to SELF_SIGNED (offline path)", authType == 3,
		"byte %d -> PlayerAuthenticationType ordinal %d", req.AuthenticationType, authType)

	check("token present (hasToken)", req.Token != "",
		"Token field empty -> NOT_AUTHENTICATED")

	// OFFLINE_CONSUMER: skipAllValidators but setRequireExpirationTime.
	tokenClaims := decodeJWTPayload(req.Token)
	check("token carries exp claim (OFFLINE_CONSUMER requires it)", tokenClaims["exp"] != nil,
		"missing exp -> InvalidJwtException -> NOT_AUTHENTICATED")

	// identityClaims: cpk claim IS the identity public key.
	cpk, _ := tokenClaims["cpk"].(string)
	check("token carries cpk claim (identity public key)", cpk != "",
		"missing cpk -> parseKey fails -> NOT_AUTHENTICATED (client JWT can never verify)")

	// Verify the client JWT signature against cpk, like jose4j does.
	sigOK, sigDetail := verifyES384(string(rawToken), cpk)
	check("client JWT signature verifies against cpk (jose4j setVerificationKey)", sigOK, sigDetail)

	claims := decodeJWTPayload(string(rawToken))
	fmt.Printf("\nclient JWT claims (%d):\n", len(claims))
	for _, name := range sortedKeys(claims) {
		v := claims[name]
		fmt.Printf("  %-34s %-12s %s\n", name, typeName(v), preview(v))
	}
	fmt.Println()

	// --- ClientChainData.from (exact port; first null return wins) ---
	chainData := replicateClientChainData(claims)

	// --- ClientSkinData.readSkin ---
	skinOK, skinDetail := replicateReadSkin(claims)
	check("ClientSkinData.readSkin returns non-null", skinOK, skinDetail)

	// --- SkinUtils.isValid ---
	valid, validDetail := replicateSkinUtilsValid(claims)
	check("SkinUtils.isValid", valid, validDetail)

	if chainData == "" {
		fmt.Println("\nRESULT: ClientChainData.from would return null -> PNX kicks INVALID_PLATFORM_SKIN (disconnectionScreen.invalidSkin)")
		failures++
	} else {
		fmt.Printf("\nClientChainData parsed OK: %s\n", chainData)
	}
	if failures > 0 {
		os.Exit(1)
	}
	fmt.Println("RESULT: every PNX login check passes locally — the payload is valid; failure must be elsewhere (server config/whitelist/xbox-auth).")
}

// check reports one replicated server check.
func check(name string, ok bool, detail string, args ...any) {
	if ok {
		fmt.Printf("PASS  %s\n", name)
		return
	}
	fmt.Printf("FAIL  %s\n", name)
	if len(args) > 0 {
		detail = fmt.Sprintf(detail, args...)
	} else {
		detail = strings.TrimSpace(detail)
	}
	fmt.Printf("      %s\n", detail)
	failures++
}

// replicateClientChainData ports PNX's ClientChainData.from. Returns a summary
// string on success or "" on the first null return, with the reason printed.
func replicateClientChainData(claims map[string]any) string {
	if len(claims) == 0 {
		fmt.Println("FAIL  ClientChainData.from: claims map is empty")
		failures++
		return ""
	}
	// Java enums resolved with from(): array-index style throws out of range
	// (caught by validateEnum -> null -> invalidSkin).
	ranges := map[string]int{
		"CurrentInputMode": 4,  // UNDEFINED MOUSE TOUCH GAME_PAD
		"DefaultInputMode": 4,  // UNDEFINED MOUSE TOUCH GAME_PAD
		"DeviceOS":         -1, // BuildPlatform.from loops; never throws
		"GraphicsMode":     4,  // SIMPLE FANCY ADVANCED RAY_TRACED
		"MemoryTier":       5,  // SUPER_LOW..SUPER_HIGH
		"PlatformType":     3,  // DESKTOP CONSOLE MOBILE
		"UIProfile":        3,  // CLASSIC POCKET NONE
	}
	for _, name := range []string{"CurrentInputMode", "DefaultInputMode", "GraphicsMode", "MemoryTier", "PlatformType", "UIProfile"} {
		v, ok := numberClaim(claims, name)
		if !ok {
			return ""
		}
		if max := ranges[name]; max >= 0 && (v < 0 || v >= float64(max)) {
			fmt.Printf("FAIL  ClientChainData.from: %s=%v out of %s enum range [0,%d) -> from() throws -> null -> INVALID_PLATFORM_SKIN\n",
				name, v, name, max)
			failures++
			return ""
		}
	}
	if _, ok := longClaim(claims, "ClientRandomId"); !ok {
		return ""
	}
	for _, name := range []string{"CompatibleWithClientSideChunkGen", "ClientIsEditorCapable", "TrustedSkin"} {
		if _, ok := booleanClaim(claims, name); !ok {
			return ""
		}
	}
	for _, name := range []string{"DeviceId", "DeviceModel", "GameVersion", "LanguageCode",
		"PlatformOfflineId", "PlatformOnlineId", "SelfSignedId", "ServerAddress", "ThirdPartyName"} {
		if _, ok := stringClaim(claims, name); !ok {
			return ""
		}
	}
	for _, name := range []string{"GuiScale", "MaxViewDistance"} {
		if _, ok := numberClaim(claims, name); !ok {
			return ""
		}
	}
	if _, ok := claims["ClientEditorConnectionIntent"]; !ok {
		fmt.Println("FAIL  ClientChainData.from: ClientEditorConnectionIntent missing -> null -> INVALID_PLATFORM_SKIN")
		failures++
		return ""
	}
	return fmt.Sprintf("input=%v deviceOS=%v platformType=%v graphics=%v memory=%v ui=%v trusted=%v",
		claims["CurrentInputMode"], claims["DeviceOS"], claims["PlatformType"],
		claims["GraphicsMode"], claims["MemoryTier"], claims["UIProfile"], claims["TrustedSkin"])
}

// replicateReadSkin ports the type-sensitive parts of ClientSkinData.readSkin.
func replicateReadSkin(claims map[string]any) (bool, string) {
	if len(claims) == 0 {
		return false, "claims map empty"
	}
	// SkinData image: Image/ImageWidth/ImageHeight via readImageData.
	w, okW := numberValue(claims["SkinImageWidth"])
	h, okH := numberValue(claims["SkinImageHeight"])
	img, okImg := claims["SkinData"].(string)
	if !okW || !okH || !okImg {
		return false, fmt.Sprintf("skin image fields: width=%v(%v) height=%v(%v) data=%v(%v)",
			claims["SkinImageWidth"], okW, claims["SkinImageHeight"], okH,
			typeName(claims["SkinData"]), okImg)
	}
	raw, err := base64.StdEncoding.DecodeString(img)
	if err != nil {
		return false, fmt.Sprintf("SkinData base64 decode: %v", err)
	}
	// Strings that must be String type if present.
	for _, name := range []string{"CapeId", "SkinGeometryData", "SkinGeometryDataEngineVersion", "SkinResourcePatch"} {
		if v, present := claims[name]; present {
			if _, ok := v.(string); !ok {
				return false, fmt.Sprintf("%s is %s, not String", name, typeName(v))
			}
		}
	}
	for _, name := range []string{"CapeOnClassicSkin", "OverrideSkin", "PersonaSkin", "PremiumSkin"} {
		if v, present := claims[name]; present {
			if _, ok := v.(bool); !ok {
				return false, fmt.Sprintf("%s is %s, not Boolean", name, typeName(v))
			}
		}
	}
	for _, name := range []string{"AnimatedImageData", "PersonaPieces", "PieceTintColors"} {
		if v, present := claims[name]; present {
			if _, ok := v.([]any); !ok {
				return false, fmt.Sprintf("%s is %s, not List", name, typeName(v))
			}
		}
	}
	return true, fmt.Sprintf("skin %vx%v %d bytes", w, h, len(raw))
}

// replicateSkinUtilsValid ports SkinUtils.isValid. SINGLE_SKIN_SIZE = 64*64*4.
func replicateSkinUtilsValid(claims map[string]any) (bool, string) {
	skinID, _ := claims["SkinId"].(string)
	if strings.TrimSpace(skinID) == "" || len(skinID) >= 100 {
		return false, fmt.Sprintf("SkinId empty or too long: %q", skinID)
	}
	w, _ := numberValue(claims["SkinImageWidth"])
	h, _ := numberValue(claims["SkinImageHeight"])
	if w < 32 || h < 32 {
		return false, fmt.Sprintf("skin image %vx%v smaller than 32x32", w, h)
	}
	raw, err := base64.StdEncoding.DecodeString(claims["SkinData"].(string))
	if err != nil {
		return false, fmt.Sprintf("SkinData base64: %v", err)
	}
	if len(raw) < 64*64*4 {
		return false, fmt.Sprintf("skin image %d bytes < SINGLE_SKIN_SIZE %d", len(raw), 64*64*4)
	}
	if v, ok := claims["SkinAnimationData"].(string); ok && len(v) >= 1000 {
		return false, "SkinAnimationData too long"
	}
	patch, ok := claims["SkinResourcePatch"].(string)
	if !ok || len(patch) > 1000 {
		return false, fmt.Sprintf("SkinResourcePatch missing/too long (%v)", typeName(claims["SkinResourcePatch"]))
	}
	decoded, err := base64.StdEncoding.DecodeString(patch)
	if err != nil {
		return false, fmt.Sprintf("SkinResourcePatch base64: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(decoded, &doc); err != nil {
		return false, fmt.Sprintf("SkinResourcePatch JSON: %v", err)
	}
	geom, ok := doc["geometry"].(map[string]any)
	if !ok {
		return false, "SkinResourcePatch has no geometry object"
	}
	def, ok := geom["default"].(string)
	if !ok || def == "" {
		return false, fmt.Sprintf("geometry.default is %s, not a string", typeName(geom["default"]))
	}
	for _, name := range []string{"AnimatedImageData", "PersonaPieces", "PieceTintColors"} {
		if list, present := claims[name].([]any); present && len(list) > 100 {
			return false, fmt.Sprintf("%s has %d entries (limit 100)", name, len(list))
		}
	}
	return true, fmt.Sprintf("skinId=%q image %vx%v patch default=%q", skinID, w, h, def)
}

// --- claim type helpers, simulating jose4j's JSON typing (integer text ->
// Long, everything else Number) ---

func numberClaim(claims map[string]any, name string) (float64, bool) {
	v, present := claims[name]
	if !present {
		fmt.Printf("FAIL  ClientChainData.from: %s missing -> null -> INVALID_PLATFORM_SKIN\n", name)
		failures++
		return 0, false
	}
	f, ok := numberValue(v)
	if !ok {
		fmt.Printf("FAIL  ClientChainData.from: %s is %s, not Number -> null -> INVALID_PLATFORM_SKIN\n", name, typeName(v))
		failures++
		return 0, false
	}
	return f, true
}

func longClaim(claims map[string]any, name string) (int64, bool) {
	v, present := claims[name]
	if !present {
		fmt.Printf("FAIL  ClientChainData.from: %s missing -> null -> INVALID_PLATFORM_SKIN\n", name)
		failures++
		return 0, false
	}
	n, ok := v.(json.Number)
	if !ok || strings.ContainsAny(n.String(), ".eE") {
		fmt.Printf("FAIL  ClientChainData.from: %s is %s (%v), not Long -> null -> INVALID_PLATFORM_SKIN\n", name, typeName(v), v)
		failures++
		return 0, false
	}
	i, err := n.Int64()
	if err != nil {
		fmt.Printf("FAIL  ClientChainData.from: %s=%v not a Long: %v\n", name, v, err)
		failures++
		return 0, false
	}
	return i, true
}

func booleanClaim(claims map[string]any, name string) (bool, bool) {
	v, present := claims[name]
	if !present {
		fmt.Printf("FAIL  ClientChainData.from: %s missing -> null -> INVALID_PLATFORM_SKIN\n", name)
		failures++
		return false, false
	}
	b, ok := v.(bool)
	if !ok {
		fmt.Printf("FAIL  ClientChainData.from: %s is %s, not Boolean -> null -> INVALID_PLATFORM_SKIN\n", name, typeName(v))
		failures++
		return false, false
	}
	return b, true
}

func stringClaim(claims map[string]any, name string) (string, bool) {
	v, present := claims[name]
	if !present {
		fmt.Printf("FAIL  ClientChainData.from: %s missing -> null -> INVALID_PLATFORM_SKIN\n", name)
		failures++
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		fmt.Printf("FAIL  ClientChainData.from: %s is %s, not String -> null -> INVALID_PLATFORM_SKIN\n", name, typeName(v))
		failures++
		return "", false
	}
	return s, true
}

func numberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func typeName(v any) string {
	if v == nil {
		return "absent"
	}
	return fmt.Sprintf("%T", v)
}

func preview(v any) string {
	switch t := v.(type) {
	case string:
		if len(t) > 48 {
			return fmt.Sprintf("%d bytes %q...", len(t), t[:45])
		}
		return strconv.Quote(t)
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case []any:
		return fmt.Sprintf("list len %d", len(t))
	case map[string]any:
		return fmt.Sprintf("object len %d", len(t))
	default:
		return fmt.Sprintf("%v", v)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// decodeJWTPayload returns the unverified claims of a compact JWT. Numbers are
// preserved as json.Number so Long-vs-Double typing can be simulated.
func decodeJWTPayload(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return map[string]any{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return map[string]any{}
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return map[string]any{}
	}
	return m
}

// verifyES384 checks the compact JWS signature of the client JWT against the
// base64 X509 public key from the cpk claim, like jose4j's setVerificationKey.
func verifyES384(token, cpk string) (bool, string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false, "client JWT is not a compact JWS"
	}
	der, err := base64.StdEncoding.DecodeString(cpk)
	if err != nil {
		return false, fmt.Sprintf("cpk base64 decode: %v", err)
	}
	pubAny, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return false, fmt.Sprintf("cpk parse: %v", err)
	}
	pub, ok := pubAny.(*ecdsa.PublicKey)
	if !ok {
		return false, fmt.Sprintf("cpk is %T, not an EC key", pubAny)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false, fmt.Sprintf("signature base64: %v", err)
	}
	if len(sig) == 0 || len(sig)%2 != 0 {
		return false, fmt.Sprintf("signature is %d bytes, want r||s", len(sig))
	}
	r := new(big.Int).SetBytes(sig[:len(sig)/2])
	s := new(big.Int).SetBytes(sig[len(sig)/2:])
	hash := sha512.Sum384([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, hash[:], r, s) {
		return false, "ecdsa.Verify rejected the signature"
	}
	return true, fmt.Sprintf("ES384 over P-%d, %d-byte signature", pub.Curve.Params().BitSize, len(sig))
}

// applyDeviceFields mirrors connection.mergeClientDataWithDevice.
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
	cd.TrustedSkin = true
	cd.PremiumSkin = true
	cd.OverrideSkin = true
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
