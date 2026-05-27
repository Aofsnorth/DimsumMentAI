package visualizer

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Server struct {
	b        *bot.Bot
	clients  map[*websocket.Conn]bool
	clientMu sync.Mutex
}

func StartServer(b *bot.Bot) {
	s := &Server{
		b:       b,
		clients: make(map[*websocket.Conn]bool),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/api/blocks", s.handleBlocks)
	mux.HandleFunc("/api/path/preview", s.handlePathPreview)
	mux.HandleFunc("/api/walk", s.handleWalk)

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			return
		}
		mux.ServeHTTP(w, r)
	}

	go s.broadcastLoop()

	go func() {
		b.Logger.Info("Starting Visualizer server on :8080")
		if err := http.ListenAndServe(":8080", http.HandlerFunc(handler)); err != nil {
			b.Logger.Error("Visualizer server failed", "error", err)
		}
	}()
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.b.Logger.Error("WebSocket upgrade failed", "error", err)
		return
	}

	s.clientMu.Lock()
	s.clients[conn] = true
	s.clientMu.Unlock()

	defer func() {
		s.clientMu.Lock()
		delete(s.clients, conn)
		s.clientMu.Unlock()
		conn.Close()
	}()

	// Send an immediate snapshot so the UI is in sync on connect.
	if msg, err := json.Marshal(s.buildState(true)); err == nil {
		_ = conn.WriteMessage(websocket.TextMessage, msg)
	}

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

type NodeData struct {
	X        int32  `json:"x"`
	Y        int32  `json:"y"`
	Z        int32  `json:"z"`
	LinkType string `json:"link"`
	Action   string `json:"action,omitempty"`
}

type BlockData struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
	Z int32 `json:"z"`
}

type statePayload struct {
	Type          string             `json:"type"`
	Timestamp     int64              `json:"ts"`
	Connected     bool               `json:"connected"`
	MovementState string             `json:"movement_state"`
	BotPos        map[string]float32 `json:"bot_pos"`
	TargetPos     map[string]float32 `json:"target_pos"`
	Path          []NodeData         `json:"path"`
	PathIndex     int                `json:"path_index"`
	Blocks        []BlockData        `json:"blocks,omitempty"`
	BlocksRadius  int                `json:"blocks_radius"`
	ServerTick    uint64             `json:"server_tick"`
}

func (s *Server) broadcastLoop() {
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	var tick uint64
	for range ticker.C {
		tick++
		includeBlocks := tick%6 == 0 // ~every 480ms
		msg, err := json.Marshal(s.buildState(includeBlocks))
		if err != nil {
			continue
		}

		s.clientMu.Lock()
		for conn := range s.clients {
			conn.SetWriteDeadline(time.Now().Add(time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				conn.Close()
				delete(s.clients, conn)
			}
		}
		s.clientMu.Unlock()
	}
}

func (s *Server) buildState(includeBlocks bool) statePayload {
	radius := 18
	s.b.Mu.Lock()
	payload := statePayload{
		Type:          "state_update",
		Timestamp:     time.Now().UnixMilli(),
		Connected:     true,
		MovementState: s.b.MovementState,
		BotPos: map[string]float32{
			"x": s.b.Pos.X(),
			"y": s.b.Pos.Y(),
			"z": s.b.Pos.Z(),
		},
		TargetPos: map[string]float32{
			"x": s.b.TargetPos.X(),
			"y": s.b.TargetPos.Y(),
			"z": s.b.TargetPos.Z(),
		},
		PathIndex:    s.b.PathIndex,
		BlocksRadius: radius,
		ServerTick:   s.b.ServerTick,
	}
	for _, n := range s.b.CurrentPath {
		payload.Path = append(payload.Path, NodeData{
			X:        n.X,
			Y:        n.Y,
			Z:        n.Z,
			LinkType: string(n.LinkType),
			Action:   n.Action,
		})
	}
	cx := int32(math.Floor(float64(s.b.Pos.X())))
	cy := int32(math.Floor(float64(s.b.Pos.Y())))
	cz := int32(math.Floor(float64(s.b.Pos.Z())))
	s.b.Mu.Unlock()

	if includeBlocks {
		payload.Blocks = s.collectBlocks(cx, cy, cz, radius)
	}
	return payload
}

func (s *Server) collectBlocks(cx, cy, cz int32, radius int) []BlockData {
	var blocks []BlockData
	for x := cx - int32(radius); x <= cx+int32(radius); x++ {
		for y := cy - int32(radius/2); y <= cy+int32(radius/2); y++ {
			for z := cz - int32(radius); z <= cz+int32(radius); z++ {
				solid := false
				if s.b.WorldCache != nil {
					if isSolid, loaded := s.b.WorldCache.IsBlockSolid(x, y, z); loaded {
						solid = isSolid
					}
				}
				if !solid {
					solid = s.b.WorldModel.IsSolid(x, y, z)
				}
				if solid {
					blocks = append(blocks, BlockData{X: x, Y: y, Z: z})
				}
			}
		}
	}
	return blocks
}

func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	radius := 18
	if val, err := strconv.Atoi(r.URL.Query().Get("radius")); err == nil {
		radius = val
	}
	if radius > 32 {
		radius = 32
	}

	s.b.Mu.Lock()
	cx := int32(math.Floor(float64(s.b.Pos.X())))
	cy := int32(math.Floor(float64(s.b.Pos.Y())))
	cz := int32(math.Floor(float64(s.b.Pos.Z())))
	s.b.Mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.collectBlocks(cx, cy, cz, radius))
}

type coordRequest struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	Z float32 `json:"z"`
}

type pathPreviewResponse struct {
	Preview PathPreviewJSON `json:"preview"`
}

type PathPreviewJSON struct {
	Start        pathfinder.Node `json:"start"`
	Target       pathfinder.Node `json:"target"`
	Path         []NodeData      `json:"path"`
	Found        bool            `json:"found"`
	UsedScaffold bool            `json:"used_scaffold"`
	UsedFallback bool            `json:"used_fallback"`
	NodeCount    int             `json:"node_count"`
}

func (s *Server) handlePathPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req coordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	preview := movement.PreviewPath(s.b, mgl32.Vec3{req.X, req.Y, req.Z})
	nodes := make([]NodeData, len(preview.Path))
	for i, n := range preview.Path {
		nodes[i] = NodeData{
			X:        n.X,
			Y:        n.Y,
			Z:        n.Z,
			LinkType: string(n.LinkType),
			Action:   n.Action,
		}
	}

	resp := pathPreviewResponse{
		Preview: PathPreviewJSON{
			Start:        preview.Start,
			Target:       preview.Target,
			Path:         nodes,
			Found:        preview.Found,
			UsedScaffold: preview.UsedScaffold,
			UsedFallback: preview.UsedFallback,
			NodeCount:    len(nodes),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleWalk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req coordRequest
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	dest := mgl32.Vec3{req.X + 0.5, req.Y, req.Z + 0.5}
	s.b.WalkTo(dest)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"target": map[string]float32{"x": dest.X(), "y": dest.Y(), "z": dest.Z()},
	})
}
