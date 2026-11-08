package live

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"
)

// Awareness (knowledge design K30) is ephemeral: a writer's cursor and name,
// relayed to the other subscriptions on this hub at once, and to other hubs in
// one notification per document per awarenessEvery on the presence
// publisher's connection, carrying the newest frame of each connection base64
// encoded. Nothing is stored, and nothing is decoded.

// maxAwarenessPayload is the encoded size at which a hub starts a new
// notification: NOTIFY refuses 8000 bytes, and maxPayload bounds the JSON.
const maxAwarenessPayload = 6 * 1024

// awarenessOverhead is the JSON around a notification's frames, generously.
const awarenessOverhead = 160

// docAwareness relays one awareness frame (K30).
func (h *Hub) docAwareness(s *docSub, data []byte) {
	if ok, _ := s.writable(); !ok {
		h.metrics.docDrop("read_only")
		return
	}
	if len(data) == 0 || len(data) > MaxDocAwareness {
		h.metrics.docDrop("awareness_too_large")
		return
	}
	if !s.c.aware.allow(h.now(), h.limits.awareRate, h.limits.awareBurst) {
		h.metrics.docDrop("awareness_rate")
		return
	}
	frame := frameDocAwareness(s.topic, data)
	for _, o := range h.docSubs(s.topic, s.c) {
		h.enqueueDroppable(o.c, frame)
	}
	h.awMu.Lock()
	pending := h.awareness[s.topic]
	if pending == nil {
		pending = map[*client][]byte{}
		h.awareness[s.topic] = pending
	}
	pending[s.c] = data
	h.awMu.Unlock()
}

// relayAwareness gives the subscriptions to topic the frames another hub
// sent, base64 encoded.
func (h *Hub) relayAwareness(topic string, encoded []string) {
	subs := h.docSubs(topic, nil)
	if len(subs) == 0 {
		return
	}
	for _, e := range encoded {
		data, err := base64.StdEncoding.DecodeString(e)
		if err != nil || len(data) == 0 || len(data) > MaxDocAwareness {
			h.metrics.docDrop("awareness_invalid")
			continue
		}
		frame := frameDocAwareness(topic, data)
		for _, s := range subs {
			h.enqueueDroppable(s.c, frame)
		}
	}
}

// awarenessLoop publishes the pending frames every awarenessEvery until ctx
// ends.
func (h *Hub) awarenessLoop(ctx context.Context) {
	ticker := time.NewTicker(h.limits.awareness)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		h.flushAwareness(ctx)
	}
}

// flushAwareness sends every document's pending awareness frames, in
// notifications under maxAwarenessPayload each.
func (h *Hub) flushAwareness(ctx context.Context) {
	h.awMu.Lock()
	batch := h.awareness
	h.awareness = map[string]map[*client][]byte{}
	h.awMu.Unlock()
	for topic, frames := range batch {
		for _, body := range awarenessBodies(topic, h.id, frames) {
			h.publish(ctx, body)
		}
	}
}

// awarenessBodies packs a document's frames into notification payloads, each
// holding at most maxAwarenessPayload bytes of encoded frames (K30).
func awarenessBodies(topic, hub string, frames map[*client][]byte) []string {
	var (
		out   []string
		chunk []string
	)
	size := awarenessOverhead + len(topic)
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		if body, err := json.Marshal(payload{T: topic, K: docAwarenessKind, H: hub, A: chunk}); err == nil {
			out = append(out, string(body))
		}
		chunk, size = nil, awarenessOverhead+len(topic)
	}
	for _, data := range frames {
		e := base64.StdEncoding.EncodeToString(data)
		if size+len(e)+3 > maxAwarenessPayload {
			flush()
		}
		chunk = append(chunk, e)
		size += len(e) + 3
	}
	flush()
	return out
}
