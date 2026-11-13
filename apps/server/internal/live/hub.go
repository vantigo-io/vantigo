package live

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/metric"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// Close codes the hub closes a socket with, beside the standard ones.
const (
	// CloseSessionEnded (4401): the session is gone, expired or idle-expired.
	CloseSessionEnded = 4401
	// CloseTooManyConnections (4429): the person already has
	// limits.connsPerUser sockets on this replica.
	CloseTooManyConnections = 4429
	// closeGoingAway (1001): the process is shutting down.
	closeGoingAway = 1001
	// closePolicy (1008): a slow consumer, three times in a minute.
	closePolicy = 1008
	// closeInvalid (1007): a binary frame that is not a frame.
	closeInvalid = 1007
	// closeTooBig (1009): a text frame over 4 KiB.
	closeTooBig = 1009
	// closeInternal (1011): a document store failed; the client reconnects
	// and sends again what was not acknowledged.
	closeInternal = 1011
	// closeNormal (1000).
	closeNormal = 1000
	// closeDead is no code on the wire: a connection that stopped answering
	// pings is dropped without a close handshake.
	closeDead = -1
)

// limits are the hub's bounds and periods (L10-L13a). A test shortens them.
type limits struct {
	queue           int           // frames per connection's send queue
	subsPerConn     int           // subscriptions per connection
	connsPerUser    int           // connections per user on this hub
	overflows       int           // slow-consumer resyncs in overflowWindow before 1008
	overflowWindow  time.Duration //
	ping            time.Duration // WebSocket ping period
	dead            time.Duration // no answer for this long closes the socket
	recheck         time.Duration // session and Authorize re-check period
	recheckBound    time.Duration // bound on one whole re-check
	listenIdle      time.Duration // the listener probes its connection after this long without a notification
	probe           time.Duration // bound on that probe
	authorize       time.Duration // bound on one Authorize call
	batch           time.Duration // presence delta batch period
	heartbeat       time.Duration // presence full-set period
	remoteExpiry    time.Duration // a remote member not mentioned for this long leaves
	pushThrottle    time.Duration // at most one presence push per topic per this
	namesTTL        time.Duration // the user directory cache
	backoffMin      time.Duration // the listener's reconnect backoff
	backoffMax      time.Duration //
	shutdownPublish time.Duration // bound on publishing the leaving members

	docRate    float64       // update frames a second per connection (K25)
	docBurst   int           // and the burst
	docBlock   time.Duration // a replay frame waits this long for room in the queue (K28)
	docReplay  time.Duration // bound on one replay
	docStore   time.Duration // bound on one store call
	awareness  time.Duration // awareness batch period (K30)
	awareRate  float64       // awareness frames a second per connection (K30)
	awareBurst int           // and the burst
}

var defaultLimits = limits{
	queue:           64,
	subsPerConn:     200,
	connsPerUser:    20,
	overflows:       3,
	overflowWindow:  time.Minute,
	ping:            25 * time.Second,
	dead:            60 * time.Second,
	recheck:         60 * time.Second,
	recheckBound:    30 * time.Second,
	listenIdle:      30 * time.Second,
	probe:           5 * time.Second,
	authorize:       10 * time.Second,
	batch:           3 * time.Second,
	heartbeat:       15 * time.Second,
	remoteExpiry:    45 * time.Second,
	pushThrottle:    500 * time.Millisecond,
	namesTTL:        10 * time.Minute,
	backoffMin:      time.Second,
	backoffMax:      30 * time.Second,
	shutdownPublish: 5 * time.Second,
	docRate:         50,
	docBurst:        200,
	docBlock:        10 * time.Second,
	docReplay:       60 * time.Second,
	docStore:        10 * time.Second,
	awareness:       250 * time.Millisecond,
	awareRate:       30,
	awareBurst:      60,
}

// connectPolicy is the rate limit on opening a socket, per user (L2).
var connectPolicy = ratelimit.Policy{Name: "live.connect", Limit: 30, Window: time.Minute}

// Options are what a hub is built from.
type Options struct {
	// DatabaseURL is what the listener and the presence publisher connect
	// to: LIVE_DATABASE_URL, else DATABASE_URL.
	DatabaseURL string
	Registry    *Registry
	Access      contracts.Access
	// Limiter limits opening a socket per user; nil limits nothing (a test).
	Limiter *ratelimit.Limiter
	// AppOrigin is APP_URL's origin, the one the handshake's Origin may be.
	AppOrigin     string
	Logger        *slog.Logger
	Clock         func() time.Time
	MeterProvider metric.MeterProvider
}

var (
	errHubClosed      = errors.New("live: the hub is closed")
	errTooManyClients = errors.New("live: too many connections for this user")
)

// Hub is one process's live fan-out: the sockets, their subscriptions, the
// listener and presence. Build it with New, run it with Start.
type Hub struct {
	id      string
	opts    Options
	reg     *Registry
	logger  *slog.Logger
	now     func() time.Time
	metrics *metrics
	limits  limits
	names   *nameCache

	// connect opens one of the hub's two dedicated connections; a seam for
	// a test.
	connect func(ctx context.Context) (*pgx.Conn, error)

	mu         sync.Mutex
	clients    map[*client]struct{}
	perUser    map[uuid.UUID]int
	subs       map[string]map[*client]struct{}
	listenerUp bool
	listenPID  uint32
	closed     bool
	presence   presenceState

	// docMu guards the fetch workers (K28);
	// awMu the pending awareness frames (K30). Neither is held with mu.
	docMu      sync.Mutex
	docWorkers map[string]*docWorker
	awMu       sync.Mutex
	awareness  map[string]map[*client][]byte

	pubMu sync.Mutex
	pub   *pgx.Conn
	// pushMu guards pushSent: the newest presence snapshot each socket was
	// given per topic, so an older snapshot never reaches it after a newer
	// one. It is held only to enqueue, never while names are resolved.
	pushMu   sync.Mutex
	pushSent map[string]map[*client]uint64

	base    context.Context // Start's, without its cancellation, for sockets
	wg      sync.WaitGroup  // every goroutine the hub started, sockets included
	stopped chan struct{}   // closed once the shutdown is done
}

// New returns a hub; it does nothing until Start.
func New(o Options) *Hub {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Registry == nil {
		o.Registry = NewRegistry()
	}
	h := &Hub{
		id:         uuid.NewString(),
		opts:       o,
		reg:        o.Registry,
		logger:     o.Logger,
		now:        o.Clock,
		metrics:    newMetrics(o.MeterProvider),
		limits:     defaultLimits,
		clients:    map[*client]struct{}{},
		perUser:    map[uuid.UUID]int{},
		subs:       map[string]map[*client]struct{}{},
		presence:   newPresenceState(),
		pushSent:   map[string]map[*client]uint64{},
		docWorkers: map[string]*docWorker{},
		awareness:  map[string]map[*client][]byte{},
		base:       context.Background(),
		stopped:    make(chan struct{}),
	}
	h.names = newNameCache(h)
	h.connect = h.dial
	return h
}

// Start brings the listener up — the first attempt before it returns, so a
// healthy hub is live at once, later ones in the background with backoff —
// and the presence publisher's loop. When ctx is cancelled the hub shuts
// down (L12): every socket is closed with 1001 and the hub's members'
// leaving is published on context.WithoutCancel. Wait waits for that.
func (h *Hub) Start(ctx context.Context) {
	h.mu.Lock()
	h.base = context.WithoutCancel(ctx)
	h.mu.Unlock()

	first, err := h.listenOnce(ctx)
	if err != nil {
		h.logger.Warn("live: the listener is down; sockets are degraded until it reconnects", "error", err)
		h.setListener(false, 0)
	}
	h.wg.Add(3)
	go func() {
		defer h.wg.Done()
		h.listen(ctx, first)
	}()
	go func() {
		defer h.wg.Done()
		h.awarenessLoop(ctx)
	}()
	go func() {
		defer h.wg.Done()
		h.presenceLoop(ctx)
	}()
	go func() {
		<-ctx.Done()
		h.shutdown()
		close(h.stopped)
	}()
}

// Wait blocks until the hub has shut down and every goroutine it started has
// returned, or ctx ends. cmd/vantigo calls it before closing the pool.
func (h *Hub) Wait(ctx context.Context) error {
	select {
	case <-h.stopped:
	case <-ctx.Done():
		return ctx.Err()
	}
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// shutdown closes every socket with 1001 and publishes the leaving members.
func (h *Hub) shutdown() {
	h.mu.Lock()
	h.closed = true
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	leaving := h.presence.leaveAll()
	h.mu.Unlock()

	for _, c := range clients {
		c.close(closeGoingAway, "going away")
	}
	if len(leaving) > 0 {
		ctx, cancel := context.WithTimeout(h.base, h.limits.shutdownPublish)
		h.publishDeltas(ctx, leaving)
		cancel()
	}
	h.pubMu.Lock()
	if h.pub != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = h.pub.Close(closeCtx)
		cancel()
		h.pub = nil
	}
	h.pubMu.Unlock()
}

// client is one socket as the hub sees it. A station's socket (§17) has the
// station's id as station, and as user too, so the per-user bounds and the
// connect rate limit are kept per station; it has no session.
type client struct {
	user, session uuid.UUID
	station       uuid.UUID
	stationIP     string // the address a station's handshake came from, for the re-check's allowlist
	stationPair   []byte // the pairing a station's handshake named, for the re-check: paired again, the socket ends
	send          chan []byte

	mu        sync.Mutex // serialises enqueue's drain and records overflows
	overflows []time.Time

	closeOnce sync.Once
	done      chan struct{}
	code      int
	reason    string

	subs  map[string]struct{} // guarded by Hub.mu
	docMu sync.Mutex          // guards docs, a leaf lock
	docs  map[string]*docSub  // the document subscriptions among subs

	// The read loop's alone: the rate limits (K25, K30) and the stray binary frames.
	writes, aware updateBucket
	strays        []time.Time
}

func (c *client) close(code int, reason string) {
	c.closeOnce.Do(func() {
		c.code, c.reason = code, reason
		close(c.done)
	})
}

func (c *client) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// attach registers a new socket for user's session, or refuses it: the hub is
// closed, or the user has reached the per-replica limit. A socket that
// arrives while the listener is down is told so at once.
func (h *Hub) attach(user, session uuid.UUID) (*client, error) {
	return h.attachClient(user, session, uuid.Nil, nil, "")
}

// attachStation registers a station's socket (§17): keyed by the station,
// with the pairing and the address its handshake came from.
func (h *Hub) attachStation(station uuid.UUID, pairing []byte, ip string) (*client, error) {
	return h.attachClient(station, uuid.Nil, station, pairing, ip)
}

func (h *Hub) attachClient(user, session, station uuid.UUID, stationPair []byte, stationIP string) (*client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errHubClosed
	}
	if h.perUser[user] >= h.limits.connsPerUser {
		return nil, errTooManyClients
	}
	c := &client{user: user, session: session, station: station, stationPair: stationPair, stationIP: stationIP, send: make(chan []byte, h.limits.queue), done: make(chan struct{}), subs: map[string]struct{}{}, docs: map[string]*docSub{}}
	h.clients[c] = struct{}{}
	h.perUser[user]++
	h.wg.Add(1) // detach's Done: Wait waits for every socket
	h.metrics.connections.Add(context.Background(), 1)
	if !h.listenerUp {
		c.send <- frameDegraded
	}
	return c, nil
}

// detach forgets a closed socket: its subscriptions and its presence.
func (h *Hub) detach(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.clients, c)
	if h.perUser[c.user]--; h.perUser[c.user] <= 0 {
		delete(h.perUser, c.user)
	}
	var changed []string
	for topic := range c.subs {
		h.dropSubLocked(c, topic)
		if h.presence.removeClient(topic, c) {
			changed = append(changed, topic)
		}
	}
	h.metrics.connections.Add(context.Background(), -1)
	for _, topic := range changed {
		h.schedulePushLocked(topic)
	}
	h.mu.Unlock()
	h.wg.Done()
}

func (h *Hub) dropSubLocked(c *client, topic string) {
	if _, ok := c.subs[topic]; !ok {
		return
	}
	delete(c.subs, topic)
	c.delDoc(topic)
	h.pushMu.Lock()
	if sent := h.pushSent[topic]; sent != nil {
		delete(sent, c)
		if len(sent) == 0 {
			delete(h.pushSent, topic)
		}
	}
	h.pushMu.Unlock()
	if set := h.subs[topic]; set != nil {
		delete(set, c)
		if len(set) == 0 {
			delete(h.subs, topic)
			delete(h.presence.seq, topic)
		}
	}
	h.metrics.subscriptions.Add(context.Background(), -1)
}

// enqueue puts frame on c's queue without ever blocking (L10): a full queue
// is cleared and a single resync queued instead, and the third time in a
// minute the socket is closed with 1008.
func (h *Hub) enqueue(c *client, frame []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isClosed() {
		return
	}
	select {
	case c.send <- frame:
		return
	default:
	}
drain:
	for {
		select {
		case <-c.send:
		default:
			break drain
		}
	}
	select {
	case c.send <- frameResyncSlow:
	default: // a replay refilled the queue meanwhile; it will be resynced by the next overflow
	}
	h.metrics.resync("slow_consumer", 1)
	// Frames of a document may have been among those discarded: say so, and
	// the client syncs again from its contiguous watermark.
	for _, s := range c.allDocs() {
		if s.stale.CompareAndSwap(false, true) {
			select {
			case c.send <- frameDocSignal("doc_stale", s.topic):
			default:
			}
		}
	}
	now := h.now()
	kept := c.overflows[:0]
	for _, at := range c.overflows {
		if now.Sub(at) < h.limits.overflowWindow {
			kept = append(kept, at)
		}
	}
	c.overflows = append(kept, now)
	if len(c.overflows) >= h.limits.overflows {
		c.close(closePolicy, "too slow")
	}
}

// inFrame is a client's frame (L4).
type inFrame struct {
	Type  string  `json:"type"`
	Topic string  `json:"topic"`
	State *string `json:"state"`
	// After is a doc_sync's: the newest seq the client holds (K28).
	After int64 `json:"after"`
	// Epoch is a doc_sync's: the epoch of the log the client holds, 0 for none.
	Epoch int64 `json:"epoch"`
	// Message is a doc_error's: what the client could not apply (K35).
	Message string `json:"message"`
}

// handleFrame acts on one text frame from c. A frame it cannot read is
// ignored: the client is ours, and a newer one may send what this hub does
// not know yet.
func (h *Hub) handleFrame(ctx context.Context, c *client, data []byte) {
	var f inFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	switch f.Type {
	case "ping":
		h.enqueue(c, framePong)
	case "sub":
		h.subscribe(ctx, c, f.Topic)
	case "unsub":
		h.unsubscribe(c, f.Topic)
	case "presence":
		h.setPresence(c, f.Topic, f.State)
	case "doc_sync":
		h.docSync(c, f.Topic, f.After, f.Epoch)
	case "doc_error":
		h.metrics.docApplyErrors.Add(context.Background(), 1)
		h.logger.Warn("live: a client could not apply a document update", "topic", f.Topic, "user", c.user, "message", truncate(f.Message, 200))
	}
}

// subscribe authorises and adds a subscription. A topic outside the grammar
// or of a kind no enabled module registered is unknown_topic; one the person
// may not see, or whose key names nothing, is forbidden — the same answer,
// so a subscription is no existence oracle (L9).
func (h *Hub) subscribe(ctx context.Context, c *client, topic string) {
	kind, key, ok := SplitTopic(topic)
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
	_, already := c.subs[topic]
	full := len(c.subs) >= h.limits.subsPerConn
	h.mu.Unlock()
	if already {
		h.enqueue(c, frameOK(topic))
		return
	}
	if full {
		h.deny(c, topic, "limit")
		return
	}
	if c.station != uuid.Nil && !t.Station {
		h.deny(c, topic, "forbidden")
		return
	}
	if err := h.authorize(ctx, newCycleAccess(h.opts.Access), c, t, key); err != nil {
		// ErrForbidden is the person's answer; anything else (a database
		// error, a timeout) is the hub's, and the client may try again.
		reason := "forbidden"
		if !errors.Is(err, ErrForbidden) {
			reason = "unavailable"
			h.logger.Warn("live: could not authorise a subscription", "kind", t.Kind, "error", err)
		}
		h.deny(c, topic, reason)
		return
	}
	var doc *docSub
	if t.Doc != nil {
		doc = &docSub{h: h, c: c, topic: topic, key: key, store: t.Doc,
			canWrite: h.canWrite(ctx, newCycleAccess(h.opts.Access), c, t, key)}
	}
	h.mu.Lock()
	if c.isClosed() || h.closed {
		h.mu.Unlock()
		return
	}
	if len(c.subs) >= h.limits.subsPerConn {
		h.mu.Unlock()
		h.deny(c, topic, "limit")
		return
	}
	if _, already := c.subs[topic]; !already {
		c.subs[topic] = struct{}{}
		if doc != nil {
			c.setDoc(topic, doc)
		}
		set := h.subs[topic]
		if set == nil {
			set = map[*client]struct{}{}
			h.subs[topic] = set
		}
		set[c] = struct{}{}
		h.metrics.subscriptions.Add(context.Background(), 1)
	}
	h.enqueue(c, frameOK(topic))
	if t.Presence {
		// The topic's set, to this subscriber alone, off the read loop:
		// resolving names reads the directory.
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			h.pushTo(topic, []*client{c})
		}()
	}
	h.mu.Unlock()
}

func (h *Hub) unsubscribe(c *client, topic string) {
	h.mu.Lock()
	h.dropSubLocked(c, topic)
	if h.presence.removeClient(topic, c) {
		h.schedulePushLocked(topic)
	}
	h.mu.Unlock()
}

// revoke drops c's subscription to topic and reports whether it still had
// one: a re-check racing the client's own unsub must not deny a topic the
// client has already left.
func (h *Hub) revoke(c *client, topic string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := c.subs[topic]; !ok {
		return false
	}
	h.dropSubLocked(c, topic)
	if h.presence.removeClient(topic, c) {
		h.schedulePushLocked(topic)
	}
	return true
}

func (h *Hub) deny(c *client, topic, reason string) {
	h.metrics.deny(reason)
	h.enqueue(c, frameDenied(topic, reason))
}

// authorize runs t's Authorize for c's user, bounded, through access.
func (h *Hub) authorize(ctx context.Context, access *cycleAccess, c *client, t Topic, key string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, h.limits.authorize)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			h.logger.Error("live: a topic's Authorize panicked", "kind", t.Kind, "panic", r)
			err = ErrForbidden
		}
	}()
	if c.station != uuid.Nil {
		if !t.Station {
			return ErrForbidden
		}
		return authorizeStation(ctx, access, c.station, t, key)
	}
	return authorizeThrough(ctx, access, c.user, t, key)
}

// AuthorizeStation runs t's Authorize for a station's socket as the hub runs
// it (§17): with StationID in the context and no person; a topic not marked
// Station refuses. A module's test uses it to prove its station topics.
func AuthorizeStation(ctx context.Context, access contracts.Access, stationID uuid.UUID, t Topic, key string) error {
	if !t.Station {
		return ErrForbidden
	}
	return authorizeStation(ctx, newCycleAccess(access), stationID, t, key)
}

func authorizeStation(ctx context.Context, access *cycleAccess, stationID uuid.UUID, t Topic, key string) error {
	return t.Authorize(context.WithValue(withAccess(ctx, access), stationContextKey{}, stationID), key)
}

// Authorize runs t's Authorize for userID with key as the hub runs it on a
// subscription: with contracts.AsUser for the person, and HasPermission
// answering through access. A module's test uses it to prove its topics.
func Authorize(ctx context.Context, access contracts.Access, userID uuid.UUID, t Topic, key string) error {
	return authorizeThrough(ctx, newCycleAccess(access), userID, t, key)
}

func authorizeThrough(ctx context.Context, access *cycleAccess, userID uuid.UUID, t Topic, key string) error {
	return t.Authorize(contracts.AsUser(withAccess(ctx, access), userID), key)
}

// recheck is the 60-second re-check (L11): the session first, without
// sliding it — a session that ended closes the socket with 4401 — then every
// subscription's Authorize again, with UserHolds cached for the cycle. One
// that now answers ErrForbidden is dropped with denied. A failure to read is
// logged and changes nothing until the next cycle.
func (h *Hub) recheck(ctx context.Context, c *client) {
	if c.station != uuid.Nil {
		// A station's socket re-checks the station: revoked, expired, paired
		// again in another browser, or its handshake's address no longer
		// inside its network allowlist, it
		// closes with 4401 (§17).
		stations, ok := h.opts.Access.(contracts.StationAccess)
		if !ok {
			c.close(CloseSessionEnded, "station ended")
			return
		}
		live, err := stations.StationLive(ctx, c.station, c.stationPair, c.stationIP)
		if err != nil {
			h.logger.Warn("live: could not re-check a station's socket", "error", err)
			return
		}
		if !live {
			c.close(CloseSessionEnded, "station ended")
			return
		}
	} else {
		user, ok, err := h.opts.Access.SessionLive(ctx, c.session)
		if err != nil {
			h.logger.Warn("live: could not re-check a socket's session", "error", err)
			return
		}
		if !ok || user != c.user {
			c.close(CloseSessionEnded, "session ended")
			return
		}
	}
	h.mu.Lock()
	topics := make([]string, 0, len(c.subs))
	for topic := range c.subs {
		topics = append(topics, topic)
	}
	h.mu.Unlock()
	access := newCycleAccess(h.opts.Access)
	for _, topic := range topics {
		kind, key, _ := SplitTopic(topic)
		t, ok := h.reg.lookup(kind)
		var err error
		if ok {
			err = h.authorize(ctx, access, c, t, key)
		}
		if ok && err != nil && !errors.Is(err, ErrForbidden) {
			h.logger.Warn("live: could not re-authorise a subscription", "kind", kind, "error", err)
			continue
		}
		if (!ok || err != nil) && h.revoke(c, topic) {
			h.deny(c, topic, "forbidden")
			continue
		}
		if ok {
			h.refreshCanWrite(ctx, access, c, topic, t, key)
		}
	}
}

// dispatch acts on one notification payload from the listener.
func (h *Hub) dispatch(raw string) {
	var p payload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		h.logger.Warn("live: a notification is not a payload", "error", err)
		return
	}
	if p.T == "" {
		if p.K == presenceKind && p.H != "" && p.H != h.id {
			h.applyRemote(p.H, p.D)
		}
		return
	}
	h.metrics.notifications.Add(context.Background(), 1)
	if kind, _, ok := SplitTopic(p.T); ok {
		if t, ok := h.reg.lookup(kind); ok && t.Doc != nil {
			h.dispatchDoc(t, p)
			return
		}
	}
	frame := frameEvent(p.T, p.K, p.ID)
	h.mu.Lock()
	targets := make([]*client, 0, len(h.subs[p.T]))
	for c := range h.subs[p.T] {
		targets = append(targets, c)
	}
	h.mu.Unlock()
	for _, c := range targets {
		h.enqueue(c, frame)
	}
}

// setListener records the listener's state. Going down tells every socket
// degraded; coming back tells every socket resync, since what was missed
// cannot be known (L7).
func (h *Hub) setListener(up bool, pid uint32) {
	h.mu.Lock()
	was := h.listenerUp
	h.listenerUp = up
	h.listenPID = pid
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	switch {
	case up && !was:
		for _, c := range clients {
			h.enqueue(c, frameResyncReconnect)
		}
		h.metrics.resync("reconnect", int64(len(clients)))
	case !up && was:
		for _, c := range clients {
			h.enqueue(c, frameDegraded)
		}
	}
}

// Health is the hub's state for the management listener.
type Health struct {
	// Listener is "up" or "down".
	Listener string
	// QueueUsage is pg_notification_queue_usage(), 0 to 1; -1 when it
	// could not be read.
	QueueUsage  float64
	Connections int
}

// Health reports the listener's state and the notification queue's usage.
func (h *Hub) Health(ctx context.Context) Health {
	h.mu.Lock()
	out := Health{Listener: "down", QueueUsage: -1, Connections: len(h.clients)}
	if h.listenerUp {
		out.Listener = "up"
	}
	h.mu.Unlock()
	h.pubMu.Lock()
	defer h.pubMu.Unlock()
	conn, err := h.publisherLocked(ctx)
	if err == nil {
		var usage float64
		if err := conn.QueryRow(ctx, "SELECT pg_notification_queue_usage()").Scan(&usage); err == nil {
			out.QueueUsage = usage
		} else {
			h.dropPublisherLocked()
		}
	}
	return out
}

// truncate cuts s to at most n bytes, for a log line.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
