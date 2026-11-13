package live

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Presence (L13a) is kept per topic in memory on every hub. A client's state
// is pushed to the hub's own subscribers at once (at most every 500 ms per
// topic); the hub tells the other hubs in one batch of deltas every 3 s and
// repeats its whole set every 15 s, on its own dedicated connection; a remote
// member not mentioned for 45 s leaves, so a hub that dies leaves no ghosts.

// rank orders the states: the strongest is shown.
func rank(state string) int {
	switch state {
	case "editing":
		return 3
	case "replying":
		return 2
	case "viewing":
		return 1
	default:
		return 0
	}
}

func validState(s string) bool { return rank(s) > 0 }

type localMember struct {
	state string
	since time.Time
}

type remoteMember struct {
	state       string
	since, seen time.Time
}

type presenceKey struct {
	topic string
	user  uuid.UUID
}

// presenceDelta is one entry of a batch: user U's state S on topic T, ""
// when they left.
type presenceDelta struct {
	T string `json:"t"`
	U string `json:"u"`
	S string `json:"s"`
}

// presenceState is guarded by Hub.mu.
type presenceState struct {
	local     map[string]map[*client]localMember
	remote    map[string]map[string]map[uuid.UUID]remoteMember // hub -> topic -> user
	announced map[string]map[uuid.UUID]string                  // topic -> user -> this hub's state for them
	pending   map[presenceKey]string
	lastPush  map[string]time.Time
	timers    map[string]*time.Timer
	seq       map[string]uint64 // per topic, one per snapshot taken
}

func newPresenceState() presenceState {
	return presenceState{
		local:     map[string]map[*client]localMember{},
		remote:    map[string]map[string]map[uuid.UUID]remoteMember{},
		announced: map[string]map[uuid.UUID]string{},
		pending:   map[presenceKey]string{},
		lastPush:  map[string]time.Time{},
		timers:    map[string]*time.Timer{},
		seq:       map[string]uint64{},
	}
}

// set records c's state on topic ("" leaves) and reports whether the local
// set changed.
func (p *presenceState) set(topic string, c *client, state string, now time.Time) bool {
	members := p.local[topic]
	old, had := members[c]
	switch {
	case state == "" && !had:
		return false
	case state == "":
		delete(members, c)
		if len(members) == 0 {
			delete(p.local, topic)
		}
	case had && old.state == state:
		return false
	default:
		if members == nil {
			members = map[*client]localMember{}
			p.local[topic] = members
		}
		members[c] = localMember{state: state, since: now}
	}
	p.reannounce(topic, c.user)
	return true
}

func (p *presenceState) removeClient(topic string, c *client) bool {
	return p.set(topic, c, "", time.Time{})
}

// reannounce records a delta when this hub's state for user on topic — the
// strongest of their sockets' — changed.
func (p *presenceState) reannounce(topic string, user uuid.UUID) {
	state := ""
	for c, m := range p.local[topic] {
		if c.user == user && rank(m.state) > rank(state) {
			state = m.state
		}
	}
	if p.announced[topic][user] == state {
		return
	}
	if state == "" {
		delete(p.announced[topic], user)
		if len(p.announced[topic]) == 0 {
			delete(p.announced, topic)
		}
	} else {
		if p.announced[topic] == nil {
			p.announced[topic] = map[uuid.UUID]string{}
		}
		p.announced[topic][user] = state
	}
	p.pending[presenceKey{topic, user}] = state
}

func (p *presenceState) takePending() []presenceDelta {
	out := make([]presenceDelta, 0, len(p.pending))
	for k, s := range p.pending {
		out = append(out, presenceDelta{T: k.topic, U: k.user.String(), S: s})
	}
	clear(p.pending)
	return out
}

func (p *presenceState) fullSet() []presenceDelta {
	var out []presenceDelta
	for topic, users := range p.announced {
		for u, s := range users {
			out = append(out, presenceDelta{T: topic, U: u.String(), S: s})
		}
	}
	return out
}

// leaveAll is every announced member leaving, for the shutdown.
func (p *presenceState) leaveAll() []presenceDelta {
	var out []presenceDelta
	for topic, users := range p.announced {
		for u := range users {
			out = append(out, presenceDelta{T: topic, U: u.String(), S: ""})
		}
	}
	clear(p.announced)
	clear(p.pending)
	clear(p.local)
	return out
}

// applyRemote applies hub's batch and returns the topics whose set changed.
func (p *presenceState) applyRemote(hub string, deltas []presenceDelta, now time.Time) []string {
	topics := p.remote[hub]
	if topics == nil {
		topics = map[string]map[uuid.UUID]remoteMember{}
		p.remote[hub] = topics
	}
	var changed []string
	for _, d := range deltas {
		user, err := uuid.Parse(d.U)
		if err != nil || !ValidTopic(d.T) || (d.S != "" && !validState(d.S)) {
			continue
		}
		users := topics[d.T]
		old, had := users[user]
		if d.S == "" {
			if had {
				delete(users, user)
				if len(users) == 0 {
					delete(topics, d.T)
				}
				changed = append(changed, d.T)
			}
			continue
		}
		if users == nil {
			users = map[uuid.UUID]remoteMember{}
			topics[d.T] = users
		}
		m := remoteMember{state: d.S, since: now, seen: now}
		if had && old.state == d.S {
			m.since = old.since
		} else {
			changed = append(changed, d.T)
		}
		users[user] = m
	}
	if len(topics) == 0 {
		delete(p.remote, hub)
	}
	return changed
}

// expire drops the remote members not mentioned within ttl and returns the
// topics whose set changed.
func (p *presenceState) expire(now time.Time, ttl time.Duration) []string {
	var changed []string
	for hub, topics := range p.remote {
		for topic, users := range topics {
			for u, m := range users {
				if now.Sub(m.seen) >= ttl {
					delete(users, u)
					changed = append(changed, topic)
				}
			}
			if len(users) == 0 {
				delete(topics, topic)
			}
		}
		if len(topics) == 0 {
			delete(p.remote, hub)
		}
	}
	return changed
}

type combined struct {
	state string
	since time.Time
}

// members is topic's set across every hub, one entry per user with their
// strongest state.
func (p *presenceState) members(topic string) map[uuid.UUID]combined {
	out := map[uuid.UUID]combined{}
	merge := func(u uuid.UUID, state string, since time.Time) {
		cur, ok := out[u]
		switch {
		case !ok || rank(state) > rank(cur.state):
			out[u] = combined{state, since}
		case rank(state) == rank(cur.state) && since.Before(cur.since):
			out[u] = combined{state, since}
		}
	}
	for c, m := range p.local[topic] {
		merge(c.user, m.state, m.since)
	}
	for _, topics := range p.remote {
		for u, m := range topics[topic] {
			merge(u, m.state, m.since)
		}
	}
	return out
}

// setPresence acts on a client's presence frame: it needs a subscription to
// a topic whose Presence is true.
func (h *Hub) setPresence(c *client, topic string, state *string) {
	if c.station != uuid.Nil {
		return // a station's socket is no person: it announces no presence (§17)
	}
	s := ""
	if state != nil {
		s = *state
		if !validState(s) {
			return
		}
	}
	h.mu.Lock()
	_, subscribed := c.subs[topic]
	h.mu.Unlock()
	if s == "" && !subscribed {
		// Leaving a topic one is not on — a client's unmount racing its
		// unsub: nothing to do, and nothing to say.
		return
	}
	kind, _, ok := SplitTopic(topic)
	if !ok {
		h.deny(c, topic, "unknown_topic")
		return
	}
	t, ok := h.reg.lookup(kind)
	if !ok {
		h.deny(c, topic, "unknown_topic")
		return
	}
	h.mu.Lock()
	_, subscribed = c.subs[topic]
	if !t.Presence || !subscribed {
		h.mu.Unlock()
		if s != "" {
			h.deny(c, topic, "forbidden")
		}
		return
	}
	if h.presence.set(topic, c, s, h.now()) {
		h.schedulePushLocked(topic)
	}
	h.mu.Unlock()
}

// applyRemote applies another hub's batch.
func (h *Hub) applyRemote(hub string, deltas []presenceDelta) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, topic := range dedupe(h.presence.applyRemote(hub, deltas, h.now())) {
		h.schedulePushLocked(topic)
	}
}

// expireRemote drops the remote members not heard of within remoteExpiry.
func (h *Hub) expireRemote() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, topic := range dedupe(h.presence.expire(h.now(), h.limits.remoteExpiry)) {
		h.schedulePushLocked(topic)
	}
}

func dedupe(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}

// schedulePushLocked pushes topic's set to its subscribers on this hub: at
// once when the last push was pushThrottle ago or more, else when it will
// be. h.mu is held.
func (h *Hub) schedulePushLocked(topic string) {
	if h.closed || len(h.subs[topic]) == 0 {
		return
	}
	p := &h.presence
	if _, scheduled := p.timers[topic]; scheduled {
		return
	}
	now := h.now()
	wait := h.limits.pushThrottle - now.Sub(p.lastPush[topic])
	if wait <= 0 {
		p.lastPush[topic] = now
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			h.pushTo(topic, nil)
		}()
		return
	}
	h.wg.Add(1)
	p.timers[topic] = time.AfterFunc(wait, func() {
		defer h.wg.Done()
		h.mu.Lock()
		delete(p.timers, topic)
		p.lastPush[topic] = h.now()
		h.mu.Unlock()
		h.pushTo(topic, nil)
	})
}

// pushTo sends topic's set to targets, or to every subscriber on this hub
// when targets is nil. The snapshot is numbered under Hub.mu and names are
// resolved with no lock held; a socket already given a newer snapshot of the
// topic is skipped, so per-topic order holds without serialising pushes.
func (h *Hub) pushTo(topic string, targets []*client) {
	h.mu.Lock()
	set := h.presence.members(topic)
	if targets == nil {
		for c := range h.subs[topic] {
			targets = append(targets, c)
		}
	}
	h.presence.seq[topic]++
	seq := h.presence.seq[topic]
	h.mu.Unlock()
	if len(targets) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(set))
	for u := range set {
		ids = append(ids, u)
	}
	ctx, cancel := context.WithTimeout(h.base, 5*time.Second)
	names := h.names.resolve(ctx, ids)
	cancel()
	members := make([]Member, 0, len(set))
	for u, m := range set {
		members = append(members, Member{UserID: u.String(), Name: names[u], State: m.state, Since: m.since.UTC().Format(time.RFC3339)})
	}
	slices.SortFunc(members, func(a, b Member) int {
		if c := strings.Compare(a.Since, b.Since); c != 0 {
			return c
		}
		return strings.Compare(a.UserID, b.UserID)
	})
	frame := framePresence(topic, members)
	h.mu.Lock()
	h.pushMu.Lock()
	sent := h.pushSent[topic]
	if sent == nil {
		sent = map[*client]uint64{}
		h.pushSent[topic] = sent
	}
	var deliver []*client
	for _, c := range targets {
		if _, subscribed := c.subs[topic]; !subscribed || sent[c] >= seq {
			continue
		}
		sent[c] = seq
		deliver = append(deliver, c)
	}
	h.mu.Unlock()
	// Enqueued under pushMu, so two pushes of one topic reach a socket in
	// the order their snapshots were taken. enqueue never blocks.
	for _, c := range deliver {
		h.enqueue(c, frame)
	}
	h.pushMu.Unlock()
}

// presenceLoop publishes this hub's deltas every batch period, its whole set
// every heartbeat, and expires remote members, until ctx ends.
func (h *Hub) presenceLoop(ctx context.Context) {
	ticker := time.NewTicker(h.limits.batch)
	defer ticker.Stop()
	var lastHeartbeat time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		h.mu.Lock()
		deltas := h.presence.takePending()
		var full []presenceDelta
		if now := h.now(); now.Sub(lastHeartbeat) >= h.limits.heartbeat {
			full = h.presence.fullSet()
			lastHeartbeat = now
		}
		h.mu.Unlock()
		h.expireRemote()
		h.publishDeltas(ctx, deltas)
		h.publishDeltas(ctx, full)
	}
}

// publishDeltas sends deltas as presence batches, each under maxPayload, on
// the publisher connection. A batch that cannot be sent is lost; the next
// heartbeat repairs it.
func (h *Hub) publishDeltas(ctx context.Context, deltas []presenceDelta) {
	if len(deltas) == 0 {
		return
	}
	const overhead = 128 // {"k":"presence","h":"<uuid>","d":[]}, generously
	var chunk []presenceDelta
	size := overhead
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		body, err := json.Marshal(payload{K: presenceKind, H: h.id, D: chunk})
		if err == nil {
			h.publish(ctx, string(body))
		}
		chunk, size = nil, overhead
	}
	for _, d := range deltas {
		n := len(d.T) + len(d.U) + len(d.S) + 24
		if size+n > maxPayload {
			flush()
		}
		chunk = append(chunk, d)
		size += n
	}
	flush()
}

// nameCache resolves user ids to display names through the registry's user
// directory, each for namesTTL.
type nameCache struct {
	h  *Hub
	mu sync.Mutex
	m  map[uuid.UUID]nameEntry
}

type nameEntry struct {
	name string
	at   time.Time
}

func newNameCache(h *Hub) *nameCache {
	return &nameCache{h: h, m: map[uuid.UUID]nameEntry{}}
}

func (n *nameCache) resolve(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]string {
	out := make(map[uuid.UUID]string, len(ids))
	now := n.h.now()
	var missing []uuid.UUID
	n.mu.Lock()
	for _, id := range ids {
		if e, ok := n.m[id]; ok && now.Sub(e.at) < n.h.limits.namesTTL {
			out[id] = e.name
		} else {
			missing = append(missing, id)
		}
	}
	n.mu.Unlock()
	dir := n.h.reg.directory()
	if len(missing) == 0 || dir == nil {
		return out
	}
	entries, err := dir.Users(ctx, missing)
	if err != nil {
		n.h.logger.Warn("live: could not resolve presence names", "error", err)
		return out
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range entries {
		out[e.ID] = e.DisplayName
		n.m[e.ID] = nameEntry{name: e.DisplayName, at: now}
	}
	return out
}
