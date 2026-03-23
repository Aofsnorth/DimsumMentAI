package queue

import (
	"errors"
	"sync"
	"time"
)

type QueueMode string

const (
	QueueModePerPlayer QueueMode = "per_player"
	QueueModeGlobal    QueueMode = "global"
	QueueModePerSession QueueMode = "per_session"
)

type QueuedMessage struct {
	PlayerID   string
	SessionID  string
	Message    string
	Timestamp  int64
	ReplyCh    chan *QueueResult
}

type QueueResult struct {
	Reply string
	Err   error
}

type MessageQueue struct {
	queues map[string]chan *QueuedMessage
	mu     sync.RWMutex
	quit   chan struct{}
	mode   QueueMode
}

func New(cfg string) *MessageQueue {
	mode := QueueMode(cfg)
	if mode != QueueModePerPlayer && mode != QueueModeGlobal && mode != QueueModePerSession {
		mode = QueueModePerPlayer
	}

	return &MessageQueue{
		queues: make(map[string]chan *QueuedMessage),
		quit:   make(chan struct{}),
		mode:   mode,
	}
}

func (q *MessageQueue) queueKey(playerID, sessionID string) string {
	switch q.mode {
	case QueueModeGlobal:
		return "global"
	case QueueModePerSession:
		if sessionID == "" {
			return playerID
		}
		return playerID + ":" + sessionID
	default:
		return playerID
	}
}

func (q *MessageQueue) getOrCreateQueue(key string) chan *QueuedMessage {
	q.mu.RLock()
	ch, exists := q.queues[key]
	q.mu.RUnlock()

	if exists {
		return ch
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if ch, exists = q.queues[key]; exists {
		return ch
	}

	ch = make(chan *QueuedMessage, 100)
	q.queues[key] = ch
	return ch
}

func (q *MessageQueue) Enqueue(msg *QueuedMessage) error {
	key := q.queueKey(msg.PlayerID, msg.SessionID)
	ch := q.getOrCreateQueue(key)

	select {
	case ch <- msg:
		return nil
	default:
		return errors.New("queue full")
	}
}

func (q *MessageQueue) Dequeue(playerID string) <-chan *QueuedMessage {
	return q.DequeueForSession(playerID, "")
}

func (q *MessageQueue) DequeueForSession(playerID, sessionID string) <-chan *QueuedMessage {
	key := q.queueKey(playerID, sessionID)
	q.mu.RLock()
	ch, exists := q.queues[key]
	q.mu.RUnlock()

	if !exists {
		q.mu.Lock()
		if ch, exists = q.queues[key]; !exists {
			ch = make(chan *QueuedMessage, 100)
			q.queues[key] = ch
		}
		q.mu.Unlock()
	}

	return ch
}

func (q *MessageQueue) Process(fn func(*QueuedMessage)) {
	q.mu.Lock()
	keys := make([]string, 0, len(q.queues))
	for key := range q.queues {
		keys = append(keys, key)
	}
	q.mu.Unlock()

	for _, key := range keys {
		go q.startWorker(key, fn)
	}
}

func (q *MessageQueue) startWorker(key string, fn func(*QueuedMessage)) {
	for {
		select {
		case msg := <-q.queues[key]:
			result := &QueueResult{}

			func() {
				defer func() {
					if r := recover(); r != nil {
						result.Err = errors.New("panic in message processing")
					}
				}()
				fn(msg)
			}()

			select {
			case msg.ReplyCh <- result:
			case <-time.After(5 * time.Second):
				result.Err = errors.New("reply timeout")
			}

		case <-q.quit:
			return
		}
	}
}

func (q *MessageQueue) Close() {
	close(q.quit)

	q.mu.Lock()
	defer q.mu.Unlock()

	for key, ch := range q.queues {
		close(ch)
		delete(q.queues, key)
	}
}

func (q *MessageQueue) Len(playerID string) int {
	return q.LenForSession(playerID, "")
}

func (q *MessageQueue) LenForSession(playerID, sessionID string) int {
	key := q.queueKey(playerID, sessionID)

	q.mu.RLock()
	ch, exists := q.queues[key]
	q.mu.RUnlock()

	if !exists {
		return 0
	}

	return len(ch)
}

func (q *MessageQueue) LenAll() map[string]int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	result := make(map[string]int, len(q.queues))
	for key, ch := range q.queues {
		result[key] = len(ch)
	}
	return result
}
