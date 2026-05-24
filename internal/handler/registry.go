package handler

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type PacketHandler interface {
	Handle(ctx context.Context, pk packet.Packet) error
}

type PacketHandlerFunc func(ctx context.Context, pk packet.Packet) error

func (f PacketHandlerFunc) Handle(ctx context.Context, pk packet.Packet) error {
	return f(ctx, pk)
}

type Registry struct {
	mu       sync.RWMutex
	handlers map[reflect.Type]PacketHandler
	fallback PacketHandler
}

func NewRegistry() *Registry {
	return &Registry{
		handlers: make(map[reflect.Type]PacketHandler),
	}
}

func (r *Registry) Register(pkType reflect.Type, handler PacketHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[pkType] = handler
}

func (r *Registry) SetFallback(handler PacketHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = handler
}

func (r *Registry) Handle(ctx context.Context, pk packet.Packet) error {
	t := reflect.TypeOf(pk)

	r.mu.RLock()
	h, ok := r.handlers[t]
	fallback := r.fallback
	r.mu.RUnlock()

	if ok {
		return h.Handle(ctx, pk)
	}

	if fallback != nil {
		return fallback.Handle(ctx, pk)
	}

	return nil
}

func ValidateRegistry(registry *Registry) error {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	if len(registry.handlers) == 0 {
		return fmt.Errorf("no packet handlers registered")
	}
	return nil
}
