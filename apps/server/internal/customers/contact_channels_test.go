package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// fakeContactChannels is Communications' contracts.ContactChannels as a test
// shapes it (Messenger design N22): the same entries for every contact it is
// asked about, and a record of the ids it was asked for.
type fakeContactChannels struct {
	entries []contracts.ContactChannelEntry
	asked   [][]int32
}

func (f *fakeContactChannels) ForContacts(_ context.Context, ids []int32) (map[int32][]contracts.ContactChannelEntry, error) {
	f.asked = append(f.asked, append([]int32(nil), ids...))
	out := map[int32][]contracts.ContactChannelEntry{}
	for _, id := range ids {
		out[id] = f.entries
	}
	return out, nil
}

type channelsJSON struct {
	Data []struct {
		Contact  contactJSON `json:"contact"`
		Channels []struct {
			ChannelId           string     `json:"channelId"`
			Type                string     `json:"type"`
			Name                string     `json:"name"`
			Linked              bool       `json:"linked"`
			ParticipantId       *string    `json:"participantId"`
			LastInboundAt       *time.Time `json:"lastInboundAt"`
			ReplyWindowClosesAt *time.Time `json:"replyWindowClosesAt"`
		} `json:"channels"`
	} `json:"data"`
}

// TestGetCustomersByIdContacts_CarriesTheContactsMessengerChannels: each contact
// row carries Communications' entries (contracts.ContactChannels) for a caller
// who may view conversations, and none for one who may not, nor without
// Communications (Deps.ContactChannels nil), nor for a customer with no contact;
// the contract is asked once for the whole list, and its entries cross as they
// are.
func TestGetCustomersByIdContacts_CarriesTheContactsMessengerChannels(t *testing.T) {
	t.Parallel()
	channel, participant := uuid.New(), uuid.New()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	closes := now.Add(20 * time.Hour)
	fake := &fakeContactChannels{entries: []contracts.ContactChannelEntry{{
		ChannelID: channel, Type: "messenger", Name: "Nordlys", Linked: true,
		ParticipantID: &participant, LastInboundAt: &now, ReplyWindowClosesAt: &closes,
	}}}
	h := newHarness(t, modtest.WithContactChannels(fake))
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Nordlys AS").Id
	first := createContact(t, c, map[string]any{"firstName": "Kari", "lastName": "Nordmann"}).Id
	second := createContact(t, c, map[string]any{"firstName": "Ola", "lastName": "Nordmann"}).Id
	attachWithRoles(t, c, customer, map[string]any{"contactId": first, "title": "Sales"})
	attachWithRoles(t, c, customer, map[string]any{"contactId": second, "title": "Ops"})
	path := fmt.Sprintf("/api/v1/customers/%d/contacts", customer)

	viewer := h.SignIn(t, "customers:contacts-view", "customers:associations-view", "communications:conversations-view")
	r := viewer.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET: status %d body %s", r.Status, r.Body)
	}
	var got channelsJSON
	r.JSON(&got)
	if len(got.Data) != 2 {
		t.Fatalf("contacts = %d, want 2", len(got.Data))
	}
	for _, row := range got.Data {
		if len(row.Channels) != 1 {
			t.Fatalf("channels of %d = %+v, want the one entry", row.Contact.Id, row.Channels)
		}
		ch := row.Channels[0]
		if ch.ChannelId != channel.String() || ch.Type != "messenger" || ch.Name != "Nordlys" || !ch.Linked ||
			ch.ParticipantId == nil || *ch.ParticipantId != participant.String() || ch.LastInboundAt == nil || ch.ReplyWindowClosesAt == nil {
			t.Errorf("channel = %+v, want the entry as Communications gave it", ch)
		}
	}
	if len(fake.asked) != 1 || len(fake.asked[0]) != 2 {
		t.Errorf("asked = %v, want one call naming both contacts", fake.asked)
	}
	if strings.Contains(string(r.Body), "psid") || strings.Contains(string(r.Body), "address") {
		t.Errorf("body %s carries a platform id", r.Body)
	}

	// Without the permission the rows are as they were, and Communications is
	// not asked.
	asked := len(fake.asked)
	plain := h.SignIn(t, "customers:contacts-view", "customers:associations-view")
	r = plain.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET without conversations-view: status %d", r.Status)
	}
	var bare channelsJSON
	r.JSON(&bare)
	for _, row := range bare.Data {
		if len(row.Channels) != 0 {
			t.Errorf("channels = %+v for a caller who may not view conversations, want none", row.Channels)
		}
	}
	if len(fake.asked) != asked {
		t.Error("Communications was asked for a caller who may not view conversations")
	}

	// A customer with no contact asks nobody.
	empty := createCustomer(t, c, "Tomt AS").Id
	if r := viewer.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/contacts", empty), nil); r.Status != http.StatusOK {
		t.Fatalf("GET empty: status %d", r.Status)
	}
	if len(fake.asked) != asked {
		t.Error("Communications was asked about a customer with no contact")
	}
}

// TestGetCustomersByIdContacts_WithoutCommunicationsShowsNoChannels: a
// deployment without Communications leaves the slot nil and the rows as they were.
func TestGetCustomersByIdContacts_WithoutCommunicationsShowsNoChannels(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Nordlys AS").Id
	contact := createContact(t, c, map[string]any{"firstName": "Kari", "lastName": "Nordmann"}).Id
	attachWithRoles(t, c, customer, map[string]any{"contactId": contact, "title": "Sales"})
	viewer := h.SignIn(t, "customers:contacts-view", "customers:associations-view", "communications:conversations-view")
	r := viewer.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/contacts", customer), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET: status %d body %s", r.Status, r.Body)
	}
	if strings.Contains(string(r.Body), `"channels"`) {
		t.Errorf("body %s carries channels without Communications", r.Body)
	}
}
