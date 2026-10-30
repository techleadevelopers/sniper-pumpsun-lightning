package events

import "sync"

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
	At   int64  `json:"at,omitempty"` // unix nano
}

type Bus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func New() *Bus {
	return &Bus{subs: make(map[chan Event]struct{})}
}

// Subscribe devolve um canal e uma função de cancelamento.
// O canal tem buffer para evitar bloquear o publisher.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 128)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// Publish envia o evento para todos os subscribers sem bloquear.
func (b *Bus) Publish(kind string, data any) {
	ev := Event{Type: kind, Data: data, At: nowNano()}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// subscriber lento: descarta o evento para não bloquear o bot
		}
	}
}

func nowNano() int64 {
	// evita importar time apenas para isto num ficheiro pequeno
	return timeNow().UnixNano()
}
