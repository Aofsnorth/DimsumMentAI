package facing_test

import (
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// --- fake bot ---

// errBoom is the transport failure the writer has to survive. A write that
// never left the bot has not been sent to anybody, and saying otherwise is how
// a build ends up reporting doors it never oriented.
type errBoom struct{}

func (errBoom) Error() string { return "connection closed" }

// fakeBot records what the writer sends instead of talking to a server. The
// only thing this package claims is what went on the wire, so that is the only
// thing the fake has to model.
type fakeBot struct {
	mu sync.Mutex

	pos      mgl32.Vec3
	writes   []packet.Packet
	lookAts  []mgl32.Vec3
	writeErr error
}

func newFakeBot() *fakeBot {
	return &fakeBot{pos: mgl32.Vec3{0, 64, 0}}
}

func (b *fakeBot) GetCoords() mgl32.Vec3 { return b.pos }

func (b *fakeBot) GetEntityRuntimeID() uint64 { return 1 }

func (b *fakeBot) GetHeldItemSlot() uint32 { return 0 }

func (b *fakeBot) WritePacket(pk packet.Packet) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.writeErr != nil {
		return b.writeErr
	}
	b.writes = append(b.writes, pk)
	return nil
}

func (b *fakeBot) LookAt(p mgl32.Vec3) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lookAts = append(b.lookAts, p)
}

// blockActorWrites returns just the BlockActorData packets, which is what a
// facing write is supposed to produce.
func (b *fakeBot) blockActorWrites() []*packet.BlockActorData {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*packet.BlockActorData, 0, len(b.writes))
	for _, pk := range b.writes {
		if bad, ok := pk.(*packet.BlockActorData); ok {
			out = append(out, bad)
		}
	}
	return out
}

// discardLogger keeps test output readable. These tests assert on payloads, not
// on log lines.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// compile-time check that the fake satisfies the interface it stands in for.
var _ interface {
	GetCoords() mgl32.Vec3
	GetEntityRuntimeID() uint64
	GetHeldItemSlot() uint32
	WritePacket(pk packet.Packet) error
	LookAt(pos mgl32.Vec3)
} = (*fakeBot)(nil)

var _ = errors.New
var _ = protocol.BlockPos{}
