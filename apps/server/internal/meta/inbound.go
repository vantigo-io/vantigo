package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/store"
)

// This file is what Communications' sync reads (Messenger design N2, N3): the
// Pages with news, the events after a cursor, an event's files and the
// acknowledgement. Acknowledging deletes nothing: acknowledged rows and their
// files stay for seven days (retention.go), so a read that failed after the
// acknowledgement can be made again.

// maxPending bounds one Pending read.
const maxPending = 200

// errAttachmentUnknown is what OpenAttachment answers for a ref that names no
// attachment of the Page: malformed, a row that is not the Page's or is gone,
// or a file the event never had.
var errAttachmentUnknown = errors.New("meta: no such attachment")

// Notified is the Pages with events not yet acknowledged that arrived after since.
func (m *messenger) Notified(ctx context.Context, since time.Time) ([]string, error) {
	pages, err := m.q.NotifiedPages(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("meta: read the notified Pages: %w", err)
	}
	return pages, nil
}

// Pending reads the Page's events after the cursor, oldest first. The Page
// must be a connected one.
func (m *messenger) Pending(ctx context.Context, address string, after int64, limit int) ([]contracts.MessengerEvent, error) {
	if err := m.connected(ctx, address); err != nil {
		return nil, err
	}
	limit = min(max(limit, 1), maxPending)
	rows, err := m.q.ListPendingInbound(ctx, store.ListPendingInboundParams{PageID: address, After: after, MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("meta: read the pending events: %w", err)
	}
	events := make([]contracts.MessengerEvent, 0, len(rows))
	for _, r := range rows {
		var p inboundPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return nil, fmt.Errorf("meta: read an inbound row's payload: %w", err)
		}
		ev := contracts.MessengerEvent{
			Cursor:            r.ID,
			Kind:              r.Kind,
			Participant:       r.Participant,
			At:                r.OccurredAt,
			PlatformMessageID: p.MID,
			Text:              p.Text,
			Echo:              p.Echo,
			Ref:               p.Ref,
		}
		if p.ReferralOnly {
			ev.Kind = contracts.MessengerEventReferral
		}
		if p.Answer != nil {
			ev.Answer = &contracts.MessengerAnswer{MessageID: p.Answer.MessageID, ChoiceID: p.Answer.ChoiceID}
		}
		if p.WatermarkMs > 0 {
			ev.Watermark = time.UnixMilli(p.WatermarkMs).UTC()
		}
		for i, a := range p.Attachments {
			ev.Attachments = append(ev.Attachments, contracts.MessengerAttachmentRef{
				Ref: strconv.FormatInt(r.ID, 10) + ":" + strconv.Itoa(i), FileName: a.FileName, ContentType: a.ContentType,
			})
		}
		events = append(events, ev)
	}
	return events, nil
}

// connected is nil for a Page that is connected and ErrMessengerEndpointUnknown
// for one that is not.
func (m *messenger) connected(ctx context.Context, address string) error {
	if _, err := m.q.GetPage(ctx, address); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contracts.ErrMessengerEndpointUnknown
		}
		return fmt.Errorf("meta: read the Page: %w", err)
	}
	return nil
}

// OpenAttachment opens one attachment of an event. ref is "<inboundId>:<index>"
// as Pending made it; one that names nothing answers errAttachmentUnknown.
func (m *messenger) OpenAttachment(ctx context.Context, address, ref string) (io.ReadCloser, contracts.MessengerFile, error) {
	idText, indexText, ok := strings.Cut(ref, ":")
	id, idErr := strconv.ParseInt(idText, 10, 64)
	index, indexErr := strconv.Atoi(indexText)
	if !ok || idErr != nil || indexErr != nil || index < 0 {
		return nil, contracts.MessengerFile{}, errAttachmentUnknown
	}
	row, err := m.q.GetInbound(ctx, store.GetInboundParams{ID: id, PageID: address})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, contracts.MessengerFile{}, errAttachmentUnknown
	case err != nil:
		return nil, contracts.MessengerFile{}, fmt.Errorf("meta: read an inbound row: %w", err)
	}
	var p inboundPayload
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		return nil, contracts.MessengerFile{}, fmt.Errorf("meta: read an inbound row's payload: %w", err)
	}
	if index >= len(row.StorageKeys) || index >= len(p.Attachments) {
		return nil, contracts.MessengerFile{}, errAttachmentUnknown
	}
	objects, err := m.objects()
	if err != nil {
		return nil, contracts.MessengerFile{}, err
	}
	body, err := objects.Get(ctx, row.StorageKeys[index])
	if err != nil {
		return nil, contracts.MessengerFile{}, fmt.Errorf("meta: open an attachment: %w", err)
	}
	a := p.Attachments[index]
	return body, contracts.MessengerFile{FileName: a.FileName, ContentType: a.ContentType, Size: a.Size}, nil
}

// Ack acknowledges the Page's events up to and including upTo. It is
// idempotent, and deletes nothing: retention does.
func (m *messenger) Ack(ctx context.Context, address string, upTo int64) error {
	now := m.now()
	if _, err := m.q.AckInbound(ctx, store.AckInboundParams{PageID: address, UpTo: upTo, Now: &now}); err != nil {
		return fmt.Errorf("meta: acknowledge the events: %w", err)
	}
	return nil
}
