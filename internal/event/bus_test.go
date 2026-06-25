package event

import (
	"reflect"
	"sync/atomic"
	"testing"
)

type testEvent struct {
	Value int
}

type anotherEvent struct {
	Msg string
}

func TestBus_PublishDeliversToSubscriber(t *testing.T) {
	t.Parallel()
	bus := NewBus()
	var received int32

	bus.Subscribe(reflect.TypeOf(testEvent{}), func(e interface{}) {
		if te, ok := e.(testEvent); ok {
			atomic.StoreInt32(&received, int32(te.Value))
		}
	})

	bus.Publish(testEvent{Value: 42})

	if got := atomic.LoadInt32(&received); got != 42 {
		t.Errorf("received = %d, want 42", got)
	}
}

func TestBus_PublishNoSubscribers(t *testing.T) {
	t.Parallel()
	bus := NewBus()
	// Should not panic when publishing an event with no subscribers.
	bus.Publish(testEvent{Value: 1})
}

func TestBus_MultipleSubscribers(t *testing.T) {
	t.Parallel()
	bus := NewBus()
	var count int32

	bus.Subscribe(reflect.TypeOf(testEvent{}), func(e interface{}) {
		atomic.AddInt32(&count, 1)
	})
	bus.Subscribe(reflect.TypeOf(testEvent{}), func(e interface{}) {
		atomic.AddInt32(&count, 1)
	})
	bus.Subscribe(reflect.TypeOf(testEvent{}), func(e interface{}) {
		atomic.AddInt32(&count, 1)
	})

	bus.Publish(testEvent{Value: 1})

	if got := atomic.LoadInt32(&count); got != 3 {
		t.Errorf("count = %d, want 3 (all subscribers called)", got)
	}
}

func TestBus_DifferentEventTypes(t *testing.T) {
	t.Parallel()
	bus := NewBus()
	var testReceived, anotherReceived int32

	bus.Subscribe(reflect.TypeOf(testEvent{}), func(e interface{}) {
		atomic.StoreInt32(&testReceived, 1)
	})
	bus.Subscribe(reflect.TypeOf(anotherEvent{}), func(e interface{}) {
		atomic.StoreInt32(&anotherReceived, 1)
	})

	bus.Publish(testEvent{Value: 1})

	if atomic.LoadInt32(&testReceived) != 1 {
		t.Error("testEvent subscriber was not called")
	}
	if atomic.LoadInt32(&anotherReceived) != 0 {
		t.Error("anotherEvent subscriber should not be called for testEvent")
	}
}

func TestBus_SubscribeAfterPublish(t *testing.T) {
	t.Parallel()
	bus := NewBus()
	bus.Publish(testEvent{Value: 1})

	var received int32
	bus.Subscribe(reflect.TypeOf(testEvent{}), func(e interface{}) {
		atomic.StoreInt32(&received, 1)
	})

	// Subscriber added after publish should not receive the past event.
	if atomic.LoadInt32(&received) != 0 {
		t.Error("subscriber added after publish should not receive past events")
	}

	// But should receive future events.
	bus.Publish(testEvent{Value: 2})
	if atomic.LoadInt32(&received) != 1 {
		t.Error("subscriber should receive future events")
	}
}

func TestBus_ConcurrentPublishAndSubscribe(t *testing.T) {
	t.Parallel()
	bus := NewBus()
	var count int32

	// Pre-subscribe one handler.
	bus.Subscribe(reflect.TypeOf(testEvent{}), func(e interface{}) {
		atomic.AddInt32(&count, 1)
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			bus.Publish(testEvent{Value: i})
		}
	}()

	// Concurrently add more subscribers.
	for i := 0; i < 10; i++ {
		bus.Subscribe(reflect.TypeOf(testEvent{}), func(e interface{}) {
			atomic.AddInt32(&count, 1)
		})
	}

	<-done
	// We can't assert an exact count due to the race between subscribing and
	// publishing, but it should be > 0 and not panic.
	if atomic.LoadInt32(&count) == 0 {
		t.Error("expected some events to be delivered")
	}
}
