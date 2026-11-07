package main

import "sync"

// Event 是推给浏览器的 SSE 事件。
type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

type subscriber struct {
	ch     chan Event
	device string
}

// Hub 维护所有 SSE 长连接。空闲连接不产生任何 CPU 开销。
type Hub struct {
	mu   sync.RWMutex
	subs map[*subscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[*subscriber]struct{}{}}
}

func (h *Hub) Subscribe(deviceID string) *subscriber {
	s := &subscriber{ch: make(chan Event, 32), device: deviceID}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) Unsubscribe(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	close(s.ch)
	h.mu.Unlock()
}

func (h *Hub) Broadcast(ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		select {
		case s.ch <- ev:
		default: // 慢客户端直接丢事件，避免阻塞广播
		}
	}
}

// Online 返回当前有长连接的设备 ID 集合。
func (h *Hub) Online() map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]bool, len(h.subs))
	for s := range h.subs {
		out[s.device] = true
	}
	return out
}
