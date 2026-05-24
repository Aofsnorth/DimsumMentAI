package event

import (
	"reflect"
	"sync"
)

type Bus struct {
	mu         sync.RWMutex
	subscribers map[reflect.Type][]func(interface{})
}

func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[reflect.Type][]func(interface{})),
	}
}

func (b *Bus) Publish(evt interface{}) {
	t := reflect.TypeOf(evt)
	b.mu.RLock()
	handlers, ok := b.subscribers[t]
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
