package calendar

import (
	"context"
	"fmt"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is the person's calendar addresses (calendar feed design D1): at
// most one per variant, full or busy, each behind a token stored only as its
// hex SHA-256 and found by it, the whole address kept sealed under
// feedAddressPurpose so the owner's own screen can show it again. GET /feed
// answers them, POST /feed/{variant} creates or resets one, DELETE turns one
// off — all calendar:access's, on the caller's own rows.

// feedAddressPurpose is the secrets box purpose a calendar address is sealed
// under.
const feedAddressPurpose = "calendar/feed-address"

// feedBusyUIDPurpose is the secrets box purpose the busy variant's UID of a
// projected item is a MAC under (feed_ics.go, busyUID).
const feedBusyUIDPurpose = "calendar/feed-busy-uid"

// feedPath is where a calendar address points below its base; the token and
// feedSuffix follow it as one path segment (D2).
const (
	feedPath   = "/api/v1/calendar/feed/"
	feedSuffix = ".ics"
)

// GetCalendarFeed The caller's calendar addresses
// (GET /api/v1/calendar/feed)
//
// Each address is opened from its sealed copy; one that cannot be opened —
// APP_SECRET rotated under it — is logged and left out, and the person
// resets it to see it again.
func (s *server) GetCalendarFeed(ctx context.Context, _ gen.GetCalendarFeedRequestObject) (gen.GetCalendarFeedResponseObject, error) {
	me := callerID(ctx)
	rows, err := s.queries().ListFeedTokens(ctx, me)
	if err != nil {
		return nil, fmt.Errorf("calendar: read the calendar addresses: %w", err)
	}
	out := gen.FeedAddresses{Public: s.feedPublic(), Addresses: []gen.FeedAddress{}}
	for _, row := range rows {
		address, err := s.deps.Secrets.OpenString(feedAddressPurpose, row.AddressCiphertext)
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "calendar: a calendar address could not be opened; left out until it is reset",
				"user_id", me.String(), "variant", row.Variant, "error", err.Error())
			continue
		}
		out.Addresses = append(out.Addresses, feedAddressOut(row, address))
	}
	return gen.GetCalendarFeed200JSONResponse(out), nil
}

// PostCalendarFeedByVariant Create or reset a calendar address
// (POST /api/v1/calendar/feed/{variant})
//
// A fresh token — the RSVP link's shape — its hash in the row and the whole
// address sealed beside it; an address the variant already had is dead from
// this moment. The token itself is in the answer and nowhere else.
func (s *server) PostCalendarFeedByVariant(ctx context.Context, req gen.PostCalendarFeedByVariantRequestObject) (gen.PostCalendarFeedByVariantResponseObject, error) {
	if !req.File.Valid() {
		return gen.PostCalendarFeedByVariant404Response{}, nil
	}
	token, err := newRSVPToken()
	if err != nil {
		return nil, err
	}
	address := s.publicBase() + feedPath + token + feedSuffix
	sealed, err := s.deps.Secrets.SealString(feedAddressPurpose, address)
	if err != nil {
		return nil, fmt.Errorf("calendar: seal a calendar address: %w", err)
	}
	row, err := s.queries().UpsertFeedToken(ctx, store.UpsertFeedTokenParams{
		UserID: callerID(ctx), Variant: string(req.File), TokenSha256: tokenSHA256(token),
		AddressCiphertext: sealed, Now: s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: write the %s calendar address: %w", req.File, err)
	}
	return gen.PostCalendarFeedByVariant200JSONResponse(feedAddressOut(row, address)), nil
}

// DeleteCalendarFeedByVariant Turn off a calendar address
// (DELETE /api/v1/calendar/feed/{variant})
func (s *server) DeleteCalendarFeedByVariant(ctx context.Context, req gen.DeleteCalendarFeedByVariantRequestObject) (gen.DeleteCalendarFeedByVariantResponseObject, error) {
	if !req.File.Valid() {
		return gen.DeleteCalendarFeedByVariant404Response{}, nil
	}
	n, err := s.queries().DeleteFeedToken(ctx, store.DeleteFeedTokenParams{UserID: callerID(ctx), Variant: string(req.File)})
	if err != nil {
		return nil, fmt.Errorf("calendar: delete the %s calendar address: %w", req.File, err)
	}
	if n == 0 {
		return gen.DeleteCalendarFeedByVariant404Response{}, nil
	}
	return gen.DeleteCalendarFeedByVariant204Response{}, nil
}

// feedPublic reports whether the addresses are on PUBLIC_BASE_URL, reachable
// from the internet, rather than on APP_URL.
func (s *server) feedPublic() bool {
	return s.deps.Config != nil && s.deps.Config.PublicBaseURL != ""
}

// feedAddressOut is one address on the wire, with its webcal form.
func feedAddressOut(row store.CalendarFeedToken, address string) gen.FeedAddress {
	return gen.FeedAddress{
		Variant:       gen.FeedVariant(row.Variant),
		Address:       address,
		WebcalAddress: webcalAddress(address),
		CreatedAt:     row.CreatedAt.UTC(),
		LastUsedAt:    utcPtr(row.LastUsedAt),
	}
}

// webcalAddress is address with webcal:// in place of its https:// or
// http://: the form Apple's and Outlook's programs subscribe to in one click.
func webcalAddress(address string) string {
	for _, scheme := range []string{"https://", "http://"} {
		if rest, ok := strings.CutPrefix(address, scheme); ok {
			return "webcal://" + rest
		}
	}
	return address
}
