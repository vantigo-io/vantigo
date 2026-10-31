package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the calendar's one workspace setting (integrations design T6):
// the channel invitations to people outside the company are sent from, or none.
// Both operations need calendar:access and calendar:manage (openapi/
// calendar.yaml's x-vantigo-access) - the module router has already checked
// them, so no handler here re-checks. The setting is the id of a Communications
// channel with no foreign key across schemas (rule 4); whether the channel
// exists and can send is Communications' answer through contracts.OutboundMail,
// read outside any transaction, and a channel that goes away leaves the id as
// it was, shown as gone, until someone chooses again. Communications never
// writes this module's settings.

// The states of the stored invitation channel, GET /invitation-settings'
// state.
const (
	stateDisabled    = gen.CalendarInvitationSettingsStateDisabled
	stateReady       = gen.CalendarInvitationSettingsStateReady
	stateUnavailable = gen.CalendarInvitationSettingsStateUnavailable
	stateGone        = gen.CalendarInvitationSettingsStateGone
)

// The title and the two sentences of a 400 on invitationChannel.
const (
	invalidInvitationSettingsTitle = "The invitation settings are not valid"
	msgInvitationChannel           = "An invitation channel is the id of a channel, or null for none"
	msgInvitationChannelUnknown    = "No active channel has this id"
)

// invitationSettingsOut renders the settings row for the wire: the stored
// channel, what it is now, and the channels a choice may name - names and
// addresses, as Communications answers them. It makes calls out of the module,
// so it runs outside any transaction.
func (s *server) invitationSettingsOut(ctx context.Context, row store.CalendarSetting) (gen.CalendarInvitationSettings, error) {
	channels, err := s.outboundChannels(ctx)
	if err != nil {
		return gen.CalendarInvitationSettings{}, fmt.Errorf("calendar: list the channels to invite from: %w", err)
	}
	list := make([]gen.CalendarSendChannel, 0, len(channels))
	state := stateDisabled
	for _, c := range channels {
		list = append(list, gen.CalendarSendChannel{
			Id: c.ID, Address: c.Address, DisplayName: c.DisplayName, Provider: c.Provider,
			ProviderDisplayName: c.ProviderDisplayName, Available: c.Available,
		})
		if row.InvitationChannel != nil && c.ID == *row.InvitationChannel {
			state = stateUnavailable
			if c.Available {
				state = stateReady
			}
		}
	}
	if row.InvitationChannel != nil && state == stateDisabled {
		state = stateGone
	}
	return gen.CalendarInvitationSettings{
		InvitationChannel: row.InvitationChannel, State: state, Channels: list,
		Revision: row.Revision, UpdatedAt: row.UpdatedAt,
	}, nil
}

// GetCalendarInvitationSettings The invitation settings
// (GET /api/v1/calendar/invitation-settings)
func (s *server) GetCalendarInvitationSettings(ctx context.Context, _ gen.GetCalendarInvitationSettingsRequestObject) (gen.GetCalendarInvitationSettingsResponseObject, error) {
	row, err := s.queries().GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("calendar: read the settings: %w", err)
	}
	out, err := s.invitationSettingsOut(ctx, row)
	if err != nil {
		return nil, err
	}
	return gen.GetCalendarInvitationSettings200JSONResponse(out), nil
}

// PutCalendarInvitationSettings Change the invitation settings
// (PUT /api/v1/calendar/invitation-settings)
//
// The setting is replaced whole under the row's lock, If-Match the revision it
// was read at. invitationChannel is required: null is off, an id must be a
// channel that exists - unless it is the stored one, so a setting whose channel
// has since gone can still be saved - else a 400 on invitationChannel. The
// channel is checked with Communications before the transaction opens.
func (s *server) PutCalendarInvitationSettings(ctx context.Context, req gen.PutCalendarInvitationSettingsRequestObject) (gen.PutCalendarInvitationSettingsResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		var f fieldErrors
		f.add("If-Match", "If-Match is the revision the setting was read at, such as 3")
		return gen.PutCalendarInvitationSettings400ApplicationProblemPlusJSONResponse(invalid(invalidInvitationSettingsTitle, f)), nil
	}
	var raw json.RawMessage
	if req.Body != nil {
		raw = req.Body.InvitationChannel
	}
	stored, err := s.queries().GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("calendar: read the settings: %w", err)
	}
	chosen, f, err := s.invitationChannelFrom(ctx, raw, stored.InvitationChannel)
	if err != nil {
		return nil, err
	}
	if f.failed() {
		return gen.PutCalendarInvitationSettings400ApplicationProblemPlusJSONResponse(invalid(invalidInvitationSettingsTitle, f)), nil
	}

	var saved store.CalendarSetting
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		current, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("calendar: lock the settings: %w", err)
		}
		if current.Revision != revision {
			return refuse(http.StatusConflict, conflict(codeRevisionMismatch, "Revision mismatch",
				fmt.Sprintf("The invitation settings have revision %d; the supplied revision was %d.", current.Revision, revision)))
		}
		saved, err = txq.SetInvitationChannel(ctx, store.SetInvitationChannelParams{InvitationChannel: chosen, Now: s.deps.Clock()})
		if err != nil {
			return fmt.Errorf("calendar: change the settings: %w", err)
		}
		return nil
	})
	if r, ok := asRefusal(err); ok {
		return gen.PutCalendarInvitationSettings409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.invitationSettingsOut(ctx, saved)
	if err != nil {
		return nil, err
	}
	return gen.PutCalendarInvitationSettings200JSONResponse(out), nil
}

// invitationChannelFrom reads the request's invitationChannel: null is off,
// and an id must be a channel that exists unless it is the stored one. A
// missing key, a malformed id or one no channel has is a field error on
// invitationChannel.
func (s *server) invitationChannelFrom(ctx context.Context, raw json.RawMessage, stored *uuid.UUID) (*uuid.UUID, fieldErrors, error) {
	var f fieldErrors
	switch {
	case len(raw) == 0:
		f.add("invitationChannel", msgInvitationChannel+"; the field is required")
		return nil, f, nil
	case string(raw) == "null":
		return nil, f, nil
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		f.add("invitationChannel", msgInvitationChannel)
		return nil, f, nil
	}
	id, err := uuid.Parse(text)
	if err != nil {
		f.add("invitationChannel", msgInvitationChannel)
		return nil, f, nil
	}
	if stored != nil && *stored == id {
		return &id, f, nil
	}
	if _, err := s.outboundChannel(ctx, id); err != nil {
		if errors.Is(err, contracts.ErrChannelUnknown) {
			f.add("invitationChannel", msgInvitationChannelUnknown)
			return nil, f, nil
		}
		return nil, f, fmt.Errorf("calendar: read the channel to invite from: %w", err)
	}
	return &id, f, nil
}
