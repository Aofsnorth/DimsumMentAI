package event

import (
	"reflect"
	"sync"
)

type Bus struct {
	mu          sync.RWMutex
	subscribers map[reflect.Type][]func(interface{})
}

func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[reflect.Type][]func(interface{})),
	}
}

func (b *Bus) Publish(evt interface{}) {
	t := reflect.TypeOf(evt)
	// Copy the handler list while the read lock is still held.
	//
	// Taking the slice and unlocking first is the same aliasing shape that
	// killed the bot in craftable.go, one severity lower because this is a
	// slice and not a map: Subscribe appends into the same backing array under
	// the write lock, and a bot reconnects and re-subscribes on every session,
	// so the append and the range really do overlap. The race detector sees it;
	// the runtime cannot catch it, which is worse in the sense that it stays
	// silent until it happens not to.
	b.mu.RLock()
	stored := b.subscribers[t]
	handlers := make([]func(interface{}), len(stored))
	copy(handlers, stored)
	ok := len(stored) > 0
	b.mu.RUnlock()

	if !ok {
		return
	}

	for _, h := range handlers {
		h(evt)
	}
}

func (b *Bus) Subscribe(eventType reflect.Type, handler func(interface{})) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers[eventType] = append(b.subscribers[eventType], handler)
}

// Unsubscribe removes a handler registered with Subscribe. It exists because a
// bot re-registers its listeners on every session: without this, each reconnect
// left the previous session's handler on the bus and one chat message was
// handled once per session ever lived.
func (b *Bus) Unsubscribe(eventType reflect.Type, handler func(interface{})) {
	b.mu.Lock()
	defer b.mu.Unlock()

	handlers := b.subscribers[eventType]
	for i, h := range handlers {
		// Comparing func values is only legal against nil, so the match is on the
		// code pointer the runtime exposes for the same function value.
		if reflect.ValueOf(h).Pointer() == reflect.ValueOf(handler).Pointer() {
			b.subscribers[eventType] = append(handlers[:i], handlers[i+1:]...)
			return
		}
	}
}
