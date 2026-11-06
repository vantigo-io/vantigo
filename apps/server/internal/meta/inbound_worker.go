package meta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/meta/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is meta-split (Messenger design N11): it turns each stored
// webhook event into the rows of meta.inbound that Communications reads. The
// webhook answered Meta without parsing anything; the parsing happens here, at
// leisure, and a payload this worker cannot read costs one event and never the
// webhook.

// The splitting worker's figures.
const (
	splitInterval = time.Second
	// splitPerCycle bounds the events one cycle takes, one claim each: an event
	// is claimed only when the worker is ready for it, so a lease never runs
	// while the event waits behind others.
	splitPerCycle = 20
	// splitLease is how long a claimed event belongs to the worker that
	// claimed it: a replica that dies lets go of it by itself. Work that can
	// outlast it (a file fetch, up to fileBudget) extends the lease first, to
	// fileBudget + splitLease, so another replica never takes an event that is
	// merely slow and attempts are not burned by it.
	splitLease = 60 * time.Second
	// maxSplitAttempts is the attempt after which an event whose attachment
	// cannot be fetched is split without it rather than lost.
	maxSplitAttempts = 5
	// splitDBBudget bounds the transaction that stores one event's rows.
	splitDBBudget = 15 * time.Second
)

// splitBackoff is the wait after the n-th failed attempt (1-based) of an event.
var splitBackoff = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

func backoffAfter(attempts int32) time.Duration {
	i := int(attempts) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(splitBackoff) {
		i = len(splitBackoff) - 1
	}
	return splitBackoff[i]
}

// inboundPayload is what meta.inbound.payload holds: the normalised event, in
// this module's own shape. Attachments are in the order of the row's
// storage_keys.
type inboundPayload struct {
	MID         string              `json:"mid,omitempty"`
	Text        string              `json:"text,omitempty"`
	Answer      *inboundAnswer      `json:"answer,omitempty"`
	Echo        bool                `json:"echo,omitempty"`
	WatermarkMs int64               `json:"watermark,omitempty"`
	Attachments []inboundAttachment `json:"attachments,omitempty"`
}

type inboundAnswer struct {
	MessageID string `json:"messageId,omitempty"`
	ChoiceID  string `json:"choiceId"`
}

type inboundAttachment struct {
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

// The webhook envelope, as much of Meta's as the splitter reads.
type whEnvelope struct {
	Object string    `json:"object"`
	Entry  []whEntry `json:"entry"`
}

type whEntry struct {
	ID        string      `json:"id"`
	Time      int64       `json:"time"`
	Messaging []whMessage `json:"messaging"`
}

type whParty struct {
	ID string `json:"id"`
}

type whMessage struct {
	Sender    whParty `json:"sender"`
	Recipient whParty `json:"recipient"`
	Timestamp int64   `json:"timestamp"`
	Message   *struct {
		MID        string `json:"mid"`
		Text       string `json:"text"`
		IsEcho     bool   `json:"is_echo"`
		QuickReply *struct {
			Payload string `json:"payload"`
		} `json:"quick_reply"`
		Attachments []struct {
			Type    string `json:"type"`
			Payload struct {
				URL string `json:"url"`
			} `json:"payload"`
		} `json:"attachments"`
	} `json:"message"`
	Postback *struct {
		MID     string `json:"mid"`
		Title   string `json:"title"`
		Payload string `json:"payload"`
	} `json:"postback"`
	Delivery *struct {
		Watermark int64 `json:"watermark"`
	} `json:"delivery"`
	Read *struct {
		Watermark int64 `json:"watermark"`
	} `json:"read"`
}

// plannedFile is one attachment of a planned row, before it is fetched.
type plannedFile struct {
	url, kind string
}

// plannedRow is one row of meta.inbound before its files are fetched.
type plannedRow struct {
	pageID      string
	kind        string
	dedupKey    string
	participant string
	at          time.Time
	payload     inboundPayload
	files       []plannedFile
}

// fetchedRow is a plannedRow with its files stored.
type fetchedRow struct {
	plannedRow
	keys []string
	skip bool
}

// SplitWorker is meta-split: it splits each stored webhook event into rows of meta.inbound.
type SplitWorker struct {
	core
	client *http.Client

	filesOnce sync.Once
	files     storage.ObjectStore
	filesErr  error
}

var _ worker.Worker = (*SplitWorker)(nil)

// NewSplitWorker builds the worker over d.
func NewSplitWorker(d module.Deps) *SplitWorker {
	return &SplitWorker{core: newCore(d), client: newHTTPClient(d, graphOrCDN, fileBudget)}
}

// Name identifies the worker in the runner's logs.
func (w *SplitWorker) Name() string { return "meta-split" }

// Interval is the worker's cycle.
func (w *SplitWorker) Interval() time.Duration { return splitInterval }

// Run is the worker loop: a cycle, then the interval. A failure is logged and
// the loop goes on.
func (w *SplitWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.ErrorContext(ctx, "meta: the splitting cycle failed", "worker", w.Name(), "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce is one cycle: it claims the due events, one at a time, and splits
// each, up to splitPerCycle. Tests drive a cycle with it.
func (w *SplitWorker) RunOnce(ctx context.Context) error {
	for range splitPerCycle {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		now := w.now()
		events, err := w.q.ClaimWebhookEvents(ctx, store.ClaimWebhookEventsParams{Now: now, LeaseUntil: now.Add(splitLease), Batch: 1})
		if err != nil {
			return fmt.Errorf("meta: claim webhook events: %w", err)
		}
		if len(events) == 0 {
			return nil
		}
		w.splitEvent(ctx, events[0])
	}
	return nil
}

func (w *SplitWorker) objects() (storage.ObjectStore, error) {
	w.filesOnce.Do(func() { w.files, w.filesErr = inboundStore(w.deps) })
	return w.files, w.filesErr
}

// splitEvent splits one claimed event and settles it: split, deferred or failed.
func (w *SplitWorker) splitEvent(ctx context.Context, e store.ClaimWebhookEventsRow) {
	var env whEnvelope
	if err := json.Unmarshal(e.Payload, &env); err != nil {
		w.settleFailed(ctx, e.ID, "the payload could not be read")
		return
	}
	rows, err := w.plan(ctx, env)
	if err != nil {
		w.settleDeferred(ctx, e, "the pages could not be read", err)
		return
	}
	fetched := make([]fetchedRow, 0, len(rows))
	for _, r := range rows {
		f, err := w.fetchFiles(ctx, e.ID, r, e.Attempts >= maxSplitAttempts)
		if err != nil {
			w.settleDeferred(ctx, e, "an attachment could not be fetched", err)
			return
		}
		fetched = append(fetched, f)
	}
	if err := w.store(ctx, e.ID, fetched); err != nil {
		w.settleDeferred(ctx, e, "the rows could not be stored", err)
	}
}

func (w *SplitWorker) settleFailed(ctx context.Context, id int64, reason string) {
	w.logger.WarnContext(ctx, "meta: a webhook event could not be split", "event", id, "reason", reason)
	if err := w.q.FailWebhookEvent(ctx, store.FailWebhookEventParams{ID: id, LastError: &reason}); err != nil {
		w.logger.WarnContext(ctx, "meta: a webhook event could not be marked failed", "event", id, "err", err)
	}
}

// settleDeferred puts the event back for a later attempt. err is logged and
// never stored: it may carry an address.
func (w *SplitWorker) settleDeferred(ctx context.Context, e store.ClaimWebhookEventsRow, reason string, err error) {
	w.logger.WarnContext(ctx, "meta: a webhook event is deferred", "event", e.ID, "attempts", e.Attempts, "reason", reason, "err", err)
	next := w.now().Add(backoffAfter(e.Attempts))
	if err := w.q.DeferWebhookEvent(ctx, store.DeferWebhookEventParams{ID: e.ID, NextAttemptAt: next, LastError: &reason}); err != nil {
		w.logger.WarnContext(ctx, "meta: a webhook event could not be deferred", "event", e.ID, "err", err)
	}
}

// plan turns the envelope into the rows to insert, dropping what is not for a
// connected Page.
func (w *SplitWorker) plan(ctx context.Context, env whEnvelope) ([]plannedRow, error) {
	if env.Object != "page" {
		return nil, nil
	}
	var rows []plannedRow
	for _, entry := range env.Entry {
		if _, err := w.q.GetPage(ctx, entry.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				w.logger.DebugContext(ctx, "meta: an event for a Page that is not connected was dropped")
				continue
			}
			return nil, err
		}
		for _, m := range entry.Messaging {
			if r, ok := w.planMessage(ctx, entry, m); ok {
				rows = append(rows, r)
			}
		}
	}
	return rows, nil
}

// planMessage is the row one messaging item becomes, if it becomes one.
func (w *SplitWorker) planMessage(ctx context.Context, entry whEntry, m whMessage) (plannedRow, bool) {
	row := plannedRow{pageID: entry.ID, at: occurredAt(m.Timestamp, entry.Time, w.now())}
	switch {
	case m.Message != nil:
		msg := m.Message
		if msg.MID == "" {
			return plannedRow{}, false
		}
		row.kind = contracts.MessengerEventMessage
		row.dedupKey = "m:" + msg.MID
		row.participant = m.Sender.ID
		row.payload = inboundPayload{MID: msg.MID, Text: msg.Text, Echo: msg.IsEcho}
		if msg.IsEcho {
			row.participant = m.Recipient.ID
		}
		for _, a := range msg.Attachments {
			switch a.Type {
			case "image", "audio", "video", "file":
				if a.Payload.URL != "" {
					row.files = append(row.files, plannedFile{url: a.Payload.URL, kind: a.Type})
				}
			}
		}
		if msg.Text == "" && len(row.files) == 0 {
			return plannedRow{}, false
		}
		if msg.QuickReply != nil && !msg.IsEcho {
			row.payload.Answer = w.quickReplyAnswer(ctx, msg.QuickReply.Payload, msg.Text)
		}
	case m.Postback != nil:
		pb := m.Postback
		row.kind = contracts.MessengerEventMessage
		row.participant = m.Sender.ID
		row.dedupKey = "p:" + pb.MID
		if pb.MID == "" {
			sum := sha256.Sum256([]byte(entry.ID + "|" + m.Sender.ID + "|" + strconv.FormatInt(m.Timestamp, 10) + "|" + pb.Payload))
			row.dedupKey = "p:" + hex.EncodeToString(sum[:])
		}
		row.payload = inboundPayload{MID: pb.MID, Text: pb.Title}
		if a, ok := contracts.ParseMessengerPayload(pb.Payload); ok {
			row.payload.Answer = &inboundAnswer{MessageID: a.MessageID, ChoiceID: a.ChoiceID}
		} else {
			w.logger.InfoContext(ctx, "meta: a postback with a payload that is not ours was read as text")
		}
		if pb.Title == "" && row.payload.Answer == nil {
			return plannedRow{}, false
		}
	case m.Delivery != nil && m.Delivery.Watermark > 0:
		row.kind = contracts.MessengerEventDelivered
		row.participant = m.Sender.ID
		row.dedupKey = "d:" + m.Sender.ID + ":" + strconv.FormatInt(m.Delivery.Watermark, 10)
		row.payload = inboundPayload{WatermarkMs: m.Delivery.Watermark}
	case m.Read != nil && m.Read.Watermark > 0:
		row.kind = contracts.MessengerEventRead
		row.participant = m.Sender.ID
		row.dedupKey = "r:" + m.Sender.ID + ":" + strconv.FormatInt(m.Read.Watermark, 10)
		row.payload = inboundPayload{WatermarkMs: m.Read.Watermark}
	default:
		return plannedRow{}, false
	}
	if row.participant == "" {
		return plannedRow{}, false
	}
	return row, true
}

// quickReplyAnswer reads a quick reply's payload: ours is an answer to a
// question; the e-mail quick reply carries the address itself as payload, which
// is not ours but looks like an address; anything else is plain text.
func (w *SplitWorker) quickReplyAnswer(ctx context.Context, payload, text string) *inboundAnswer {
	if a, ok := contracts.ParseMessengerPayload(payload); ok {
		return &inboundAnswer{MessageID: a.MessageID, ChoiceID: a.ChoiceID}
	}
	if looksLikeEmail(text) {
		return &inboundAnswer{ChoiceID: contracts.MessengerShareEmail}
	}
	w.logger.InfoContext(ctx, "meta: a quick reply with a payload that is not ours was read as text")
	return nil
}

// looksLikeEmail is whether s has one "@", no spaces and something either side.
func looksLikeEmail(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && at < len(s)-1 && strings.Count(s, "@") == 1 && !strings.ContainsAny(s, " \t\r\n")
}

// occurredAt is the event's time: its own timestamp in ms, else the entry's,
// else fallback.
func occurredAt(ts, entryTime int64, fallback time.Time) time.Time {
	switch {
	case ts > 0:
		return time.UnixMilli(ts).UTC()
	case entryTime > 0:
		return time.UnixMilli(entryTime).UTC()
	}
	return fallback
}

// fetchFiles fetches the row's attachments from Meta's CDN and stores them,
// before any transaction: the URLs expire, and no provider call or storage
// write happens inside a transaction. A failure is returned — the event is
// deferred — unless last is set, when the file is left out of the row.
func (w *SplitWorker) fetchFiles(ctx context.Context, eventID int64, r plannedRow, last bool) (fetchedRow, error) {
	out := fetchedRow{plannedRow: r}
	out.payload.Attachments = nil
	sum := sha256.Sum256([]byte(r.dedupKey))
	prefix := r.pageID + "/" + hex.EncodeToString(sum[:])[:32] + "/"
	for i, f := range r.files {
		key := prefix + strconv.Itoa(i)
		att, err := w.fetchOne(ctx, eventID, f, key, i)
		if err != nil {
			if !last {
				return fetchedRow{}, err
			}
			w.logger.WarnContext(ctx, "meta: an attachment could not be fetched and was left out", "err", err)
			continue
		}
		out.payload.Attachments = append(out.payload.Attachments, att)
		out.keys = append(out.keys, key)
	}
	if r.payload.Text == "" && len(r.files) > 0 && len(out.keys) == 0 {
		// A message of nothing but files, none of which could be kept, says nothing.
		out.skip = true
	}
	return out, nil
}

// fetchOne fetches one file, refuses more than the attachment limit and keeps
// it under key. The event's lease is extended first, so the fetch may take its
// whole budget without another replica claiming the event; and the key is
// staged before the write, so a file whose transaction never happens is found
// by retention (see store).
func (w *SplitWorker) fetchOne(ctx context.Context, eventID int64, f plannedFile, key string, index int) (inboundAttachment, error) {
	objects, err := w.objects()
	if err != nil {
		return inboundAttachment{}, err
	}
	if err := w.q.ExtendWebhookEventLease(ctx, store.ExtendWebhookEventLeaseParams{ID: eventID, LeaseUntil: w.now().Add(fileBudget + splitLease)}); err != nil {
		return inboundAttachment{}, fmt.Errorf("meta: extend an event's lease: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return inboundAttachment{}, errors.New("meta: the attachment address is not usable")
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return inboundAttachment{}, fmt.Errorf("meta: fetch an attachment: %w", scrubURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return inboundAttachment{}, fmt.Errorf("meta: fetch an attachment: status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxAttachmentBytes {
		return inboundAttachment{}, errors.New("meta: an attachment is over the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
	if err != nil {
		return inboundAttachment{}, fmt.Errorf("meta: read an attachment: %w", scrubURL(err))
	}
	if int64(len(data)) > maxAttachmentBytes {
		return inboundAttachment{}, errors.New("meta: an attachment is over the size limit")
	}
	contentType := fallbackContentType(f.kind)
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && mt != "" {
		contentType = safeContentType(mt)
	}
	if err := w.q.StageFile(ctx, store.StageFileParams{StorageKey: key, Now: w.now()}); err != nil {
		return inboundAttachment{}, fmt.Errorf("meta: stage an attachment: %w", err)
	}
	if err := objects.Put(ctx, key, bytes.NewReader(data), contentType); err != nil {
		return inboundAttachment{}, fmt.Errorf("meta: keep an attachment: %w", err)
	}
	return inboundAttachment{FileName: fileNameOf(f.url, contentType, index), ContentType: contentType, Size: int64(len(data))}, nil
}

// storedContentTypes are the types kept as the CDN gave them. Anything else
// (text/html, image/svg+xml, ...) is a file a browser might run, and is stored
// as application/octet-stream.
var storedContentTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/heic": true,
	"video/mp4": true, "video/quicktime": true, "video/webm": true,
	"audio/mpeg": true, "audio/mp4": true, "audio/aac": true, "audio/ogg": true, "audio/wav": true, "audio/webm": true,
	"application/pdf": true, "application/msword": true, "application/vnd.ms-excel": true, "application/vnd.ms-powerpoint": true,
	"text/plain": true, "text/csv": true, "application/zip": true,
}

// safeContentType is mt (parameters already stripped by mime.ParseMediaType)
// when it is in the allow-list, else application/octet-stream.
func safeContentType(mt string) string {
	if storedContentTypes[mt] || strings.HasPrefix(mt, "application/vnd.openxmlformats-officedocument.") {
		return mt
	}
	return "application/octet-stream"
}

// scrubURL drops the URL a transport error carries: it is the person's file's
// address, and the logs hold none.
func scrubURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func fallbackContentType(kind string) string {
	switch kind {
	case "image":
		return "image/jpeg"
	case "audio":
		return "audio/mpeg"
	case "video":
		return "video/mp4"
	}
	return "application/octet-stream"
}

var extensions = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	"audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/aac": ".aac", "audio/ogg": ".ogg", "audio/wav": ".wav",
	"video/mp4": ".mp4", "video/quicktime": ".mov", "video/webm": ".webm",
	"application/pdf": ".pdf",
}

// fileNameOf is the last segment of the file's address, or attachment-N, with
// an extension for the content type when it has none.
func fileNameOf(rawURL, contentType string, index int) string {
	name := ""
	if u, err := url.Parse(rawURL); err == nil {
		name = path.Base(u.Path)
		if unescaped, err := url.PathUnescape(name); err == nil {
			name = unescaped
		}
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if name == "." || name == ".." || name == "/" {
		name = ""
	}
	if r := []rune(name); len(r) > 100 {
		name = string(r[:100])
	}
	if name == "" {
		name = "attachment-" + strconv.Itoa(index)
	}
	if path.Ext(name) == "" {
		name += extensions[contentType]
	}
	return name
}

// store writes the event's rows in one transaction and marks it split. The
// lock first serialises the transactions that insert into meta.inbound: the
// rows' ids are a bigserial handed out at insert time, and without the lock a
// transaction that took id 7 could commit after one that took id 8, so a
// reader at cursor 8 would skip 7 for good. Holding the advisory lock from the
// first insert to the commit makes ids commit in order.
//
// Files were written before this transaction, their keys staged in
// meta.pending_files. A row's keys are unstaged here, in the transaction that
// inserts the row that names them; a key whose transaction rolls back or never
// runs stays staged, and retention deletes the object after a day. A row for a
// Page removed meanwhile is not inserted (the Page row is read FOR SHARE, so
// Remove Page cannot slip in before the commit), and its files are deleted
// after the commit.
func (w *SplitWorker) store(ctx context.Context, eventID int64, rows []fetchedRow) error {
	var orphans []string
	err := func() error {
		ctx, cancel := context.WithTimeout(ctx, splitDBBudget)
		defer cancel()
		return db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			orphans = nil
			q := w.q.WithTx(tx)
			if err := q.LockInbound(ctx); err != nil {
				return err
			}
			now := w.now()
			var kept []string
			for _, r := range rows {
				if r.skip {
					continue
				}
				if _, err := q.PageExistsForShare(ctx, r.pageID); err != nil {
					if !errors.Is(err, pgx.ErrNoRows) {
						return err
					}
					orphans = append(orphans, r.keys...)
					continue
				}
				payload, err := json.Marshal(r.payload)
				if err != nil {
					return err
				}
				keys := r.keys
				if keys == nil {
					keys = []string{}
				}
				if _, err := q.InsertInbound(ctx, store.InsertInboundParams{
					PageID: r.pageID, Kind: r.kind, DedupKey: r.dedupKey, Participant: r.participant,
					OccurredAt: r.at, Payload: payload, StorageKeys: keys, Now: now,
				}); err != nil {
					return err
				}
				kept = append(kept, keys...)
			}
			if err := q.UnstageFiles(ctx, kept); err != nil {
				return err
			}
			return q.MarkWebhookEventSplit(ctx, store.MarkWebhookEventSplitParams{ID: eventID, Now: &now})
		})
	}()
	if err != nil || len(orphans) == 0 {
		return err
	}
	w.dropOrphans(ctx, orphans)
	return nil
}

// dropOrphans deletes the files of rows that were not inserted and unstages
// their keys. A file that cannot be deleted stays staged for retention.
func (w *SplitWorker) dropOrphans(ctx context.Context, keys []string) {
	objects, err := w.objects()
	if err != nil {
		w.logger.WarnContext(ctx, "meta: the files of a removed Page's event could not be deleted", "err", err)
		return
	}
	var gone []string
	for _, k := range keys {
		if err := objects.Delete(ctx, k); err != nil && !errors.Is(err, storage.ErrNotExist) {
			w.logger.WarnContext(ctx, "meta: a file of a removed Page's event could not be deleted; retention will", "err", err)
			continue
		}
		gone = append(gone, k)
	}
	if err := w.q.UnstageFiles(ctx, gone); err != nil {
		w.logger.WarnContext(ctx, "meta: staged files could not be cleared", "err", err)
	}
}
