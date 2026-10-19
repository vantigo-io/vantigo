package calendar

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is an appointment's attendees (calendar invitations design D1):
// three kinds on one table — a colleague (user), a customer's contact
// (contact, an opaque id named and addressed through the customer directory
// at read and at send time, never copied) and an address typed in (email).
// The two outside kinds carry an RSVP token, stored as its hash, and the link
// that carries it, sealed, so a resend sends the same one (D5). A contact's
// address is never on the wire and never in a log; an email attendee's is
// what the writer typed, and is on the appointment.

// The attendee kinds, as stored and on the wire.
const (
	kindUser    = "user"
	kindContact = "contact"
	kindEmail   = "email"
)

// The bounds of an email attendee: RFC 5321's longest path, and the name.
const (
	maxEmailLength = 254
	maxNameLength  = 200
)

// rsvpLinkPurpose is the secrets box purpose an RSVP link is sealed under.
const rsvpLinkPurpose = "calendar/rsvp-link"

// The customer directory's keys a contact attendee needs (D1, D2): the
// writer names a contact only holding contacts-view, the key that guards its
// address in Customers; the picker lists them under that and
// associations-view, the pair Customers' own list requires.
const (
	permContactsView     = "customers:contacts-view"
	permAssociationsView = "customers:associations-view"
)

// attendeeSet is an AppointmentWrite's attendees once each passed its rule,
// each named once, never nil, so "none" removes every attendee.
type attendeeSet struct {
	users    []uuid.UUID
	contacts []int32
	emails   []emailAttendee
}

// emailAttendee is an address typed in, lower-cased, with the name as typed.
type emailAttendee struct {
	address string
	name    *string
}

func (a attendeeSet) count() int { return len(a.users) + len(a.contacts) + len(a.emails) }

// people is the names an appointment's response shows: the owner's and every
// user attendee's display name, and every contact attendee as the directory
// answers it — nil for one it answers nothing for.
type people struct {
	users    map[uuid.UUID]string
	contacts map[int32]*contracts.ContactEntry
}

// isOutside reports whether an attendee is mailed an invitation: a contact or
// an address, never a colleague.
func isOutside(at store.CalendarAppointmentAttendee) bool { return at.Kind != kindUser }

// contactName is a contact's name as the appointment shows it, nil when it
// has none.
func contactName(c *contracts.ContactEntry) *string {
	if c == nil {
		return nil
	}
	name := strings.TrimSpace(strings.TrimSpace(c.FirstName) + " " + strings.TrimSpace(c.LastName))
	if name == "" {
		return nil
	}
	return &name
}

// readAttendees holds the attendee list to D1: the owner never among them; a
// user active in the directory, or one already on the appointment; a contact
// the directory answers, with an address, named by a writer who holds
// customers:contacts-view — or one already on the appointment, whom a PUT
// keeps whatever the directory now says, so an erased contact does not lock
// the owner out of their appointment; an address net/mail accepts, at most
// 254 characters, with a name of at most 200. Two naming the same user,
// contact or address are one, and at most 50 remain. An element is refused by
// its index in the request. It answers the names for the response, read
// from the directories before any transaction.
//
// current is the appointment's attendees as they stand, on a PUT by its
// owner; nil on a create.
func (s *server) readAttendees(ctx context.Context, raw *[]gen.AttendeeWrite, owner uuid.UUID, current []store.CalendarAppointmentAttendee, in *appointmentInput, f *fieldErrors) (people, error) {
	var list []gen.AttendeeWrite
	if raw != nil {
		list = *raw
	}
	keptUsers, keptContacts := map[uuid.UUID]bool{}, map[int32]bool{}
	for _, at := range current {
		switch {
		case at.UserID != nil:
			keptUsers[*at.UserID] = true
		case at.ContactID != nil:
			keptContacts[*at.ContactID] = true
		}
	}
	userIndex, contactIndex, emailSeen := map[uuid.UUID]int{}, map[int32]int{}, map[string]bool{}
	for i, a := range list {
		field := fmt.Sprintf("attendees[%d]", i)
		switch a.Kind {
		case gen.User:
			switch {
			case a.UserId == nil:
				f.add(field, "userId is required")
			case a.ContactId != nil || a.Email != nil || a.Name != nil:
				f.add(field, "A colleague is named by userId alone")
			case *a.UserId == owner:
				f.add(field, "The owner is not one of the attendees")
			default:
				if _, seen := userIndex[*a.UserId]; !seen {
					userIndex[*a.UserId] = i
					in.attendees.users = append(in.attendees.users, *a.UserId)
				}
			}
		case gen.Contact:
			switch {
			case a.ContactId == nil:
				f.add(field, "contactId is required")
			case a.UserId != nil || a.Email != nil || a.Name != nil:
				f.add(field, "A contact is named by contactId alone")
			default:
				if _, seen := contactIndex[*a.ContactId]; !seen {
					contactIndex[*a.ContactId] = i
					in.attendees.contacts = append(in.attendees.contacts, *a.ContactId)
				}
			}
		case gen.Email:
			e, msg := readEmailAttendee(a)
			if msg != "" {
				f.add(field, msg)
				continue
			}
			if !emailSeen[e.address] {
				emailSeen[e.address] = true
				in.attendees.emails = append(in.attendees.emails, e)
			}
		default:
			f.add(field, "kind is user, contact or email")
		}
	}
	if in.attendees.count() > maxAttendees {
		f.add("attendees", fmt.Sprintf("An appointment has at most %d attendees", maxAttendees))
		in.attendees = attendeeSet{}
		userIndex, contactIndex = map[uuid.UUID]int{}, map[int32]int{}
	}

	found, err := s.usersBatch(ctx, append([]uuid.UUID{owner}, in.attendees.users...))
	if err != nil {
		return people{}, fmt.Errorf("calendar: read the attendees: %w", err)
	}
	ppl := people{users: map[uuid.UUID]string{}, contacts: map[int32]*contracts.ContactEntry{}}
	active := map[uuid.UUID]bool{}
	for _, u := range found {
		ppl.users[u.ID] = u.DisplayName
		active[u.ID] = u.Active
	}
	for _, id := range in.attendees.users {
		if _, known := ppl.users[id]; known && (active[id] || keptUsers[id]) {
			continue
		}
		f.add(fmt.Sprintf("attendees[%d]", userIndex[id]), "No active user has this id")
	}

	mayName := len(in.attendees.contacts) > 0 && s.has(ctx, permContactsView)
	for _, id := range in.attendees.contacts {
		c, err := s.directoryContact(ctx, id)
		if err != nil {
			return people{}, fmt.Errorf("calendar: read contact %d: %w", id, err)
		}
		ppl.contacts[id] = c
		if keptContacts[id] {
			continue
		}
		field := fmt.Sprintf("attendees[%d]", contactIndex[id])
		switch {
		case s.deps.Directory == nil:
			f.add(field, "Customers is not enabled, so no contact can be invited")
		case !mayName:
			f.add(field, "Inviting a contact needs customers:contacts-view")
		case c == nil:
			f.add(field, "No contact has this id")
		case c.Email == nil || strings.TrimSpace(*c.Email) == "":
			f.add(field, "This contact has no email address to send the invitation to")
		}
	}
	return ppl, nil
}

// readEmailAttendee is an email attendee as stored — the address net/mail
// accepts, lower-cased, and the name trimmed, absent when blank — or the
// message that refuses it.
func readEmailAttendee(a gen.AttendeeWrite) (emailAttendee, string) {
	if a.UserId != nil || a.ContactId != nil {
		return emailAttendee{}, "An address is named by email and name alone"
	}
	if a.Email == nil || strings.TrimSpace(*a.Email) == "" {
		return emailAttendee{}, "email is required"
	}
	raw := strings.TrimSpace(*a.Email)
	if utf8.RuneCountInString(raw) > maxEmailLength {
		return emailAttendee{}, fmt.Sprintf("An address is at most %d characters", maxEmailLength)
	}
	parsed, err := mail.ParseAddress(raw)
	if err != nil || parsed.Name != "" || parsed.Address != raw {
		return emailAttendee{}, "This is not an email address"
	}
	e := emailAttendee{address: strings.ToLower(parsed.Address)}
	if a.Name != nil {
		name := strings.TrimSpace(*a.Name)
		if utf8.RuneCountInString(name) > maxNameLength {
			return emailAttendee{}, fmt.Sprintf("A name is at most %d characters", maxNameLength)
		}
		if name != "" {
			e.name = &name
		}
	}
	return e, ""
}

// currentAttendees is the appointment's attendees as they stand, read before
// a PUT's transaction, when me owns it — for no one else, so a PUT cannot
// probe who attends someone else's appointment. The owner's own row lock
// later decides the write; this only relaxes the directory checks for an
// attendee already on it.
func (s *server) currentAttendees(ctx context.Context, id int64, me uuid.UUID) ([]store.CalendarAppointmentAttendee, error) {
	q := s.queries()
	a, err := q.GetAppointment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && a.OwnerUserID != me) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d: %w", id, err)
	}
	atts, err := q.ListAttendees(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d's attendees: %w", id, err)
	}
	return atts, nil
}

// insertAttendees adds each attendee of set not already on the appointment,
// at needs-action, an outside one with a fresh RSVP token and its sealed
// link; one already there keeps their row, their answer and their token. It
// answers the rows it added.
func (s *server) insertAttendees(ctx context.Context, txq *store.Queries, id int64, set attendeeSet, now time.Time) ([]store.CalendarAppointmentAttendee, error) {
	var added []store.CalendarAppointmentAttendee
	keep := func(row store.CalendarAppointmentAttendee, err error) error {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("calendar: add appointment %d's attendees: %w", id, err)
		}
		added = append(added, row)
		return nil
	}
	for _, u := range set.users {
		if err := keep(txq.InsertUserAttendee(ctx, store.InsertUserAttendeeParams{AppointmentID: id, UserID: u, Now: now})); err != nil {
			return nil, err
		}
	}
	for _, c := range set.contacts {
		hash, sealed, err := s.newRSVPLink()
		if err != nil {
			return nil, err
		}
		if err := keep(txq.InsertContactAttendee(ctx, store.InsertContactAttendeeParams{
			AppointmentID: id, ContactID: c, RsvpTokenSha256: hash, RsvpLinkCiphertext: sealed, Now: now,
		})); err != nil {
			return nil, err
		}
	}
	for _, e := range set.emails {
		hash, sealed, err := s.newRSVPLink()
		if err != nil {
			return nil, err
		}
		if err := keep(txq.InsertEmailAttendee(ctx, store.InsertEmailAttendeeParams{
			AppointmentID: id, Email: e.address, Name: e.name, RsvpTokenSha256: hash, RsvpLinkCiphertext: sealed, Now: now,
		})); err != nil {
			return nil, err
		}
	}
	return added, nil
}

// dropUnlisted removes every attendee of the appointment that set does not
// name — all of them when it is empty — and answers the rows it removed, with
// their sealed links, so a dropped outside attendee can be sent its CANCEL.
func dropUnlisted(ctx context.Context, txq *store.Queries, id int64, set attendeeSet) ([]store.CalendarAppointmentAttendee, error) {
	atts, err := txq.ListAttendees(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("calendar: read appointment %d's attendees: %w", id, err)
	}
	users, contacts, emails := map[uuid.UUID]bool{}, map[int32]bool{}, map[string]bool{}
	for _, u := range set.users {
		users[u] = true
	}
	for _, c := range set.contacts {
		contacts[c] = true
	}
	for _, e := range set.emails {
		emails[e.address] = true
	}
	keep := []int64{}
	for _, at := range atts {
		switch {
		case at.Kind == kindUser && at.UserID != nil && users[*at.UserID],
			at.Kind == kindContact && at.ContactID != nil && contacts[*at.ContactID],
			at.Kind == kindEmail && at.Email != nil && emails[*at.Email]:
			keep = append(keep, at.ID)
		}
	}
	dropped, err := txq.DeleteAttendeesNotIn(ctx, store.DeleteAttendeesNotInParams{AppointmentID: id, Ids: keep})
	if err != nil {
		return nil, fmt.Errorf("calendar: remove appointment %d's attendees: %w", id, err)
	}
	return dropped, nil
}

// newRSVPToken is a fresh RSVP token: 32 random bytes, base64url without
// padding (D5, the pay link's shape).
func newRSVPToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("calendar: draw an RSVP token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// tokenSHA256 is a token's hex SHA-256, the column an attendee is found by.
// The token itself is never stored.
func tokenSHA256(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// rsvpBase is where the RSVP page is: the public host when PUBLIC_BASE_URL is
// set, reachable by anyone, else APP_URL with the base path — sessionless
// all the same (D3).
func (s *server) rsvpBase() string {
	cfg := s.deps.Config
	if cfg == nil {
		return ""
	}
	if cfg.PublicBaseURL != "" {
		return cfg.PublicBaseURL
	}
	return cfg.AppOrigin + cfg.BasePath
}

// newRSVPLink is a new outside attendee's token, as the hash the row stores
// and the link sealed under rsvpLinkPurpose. Sealing is the in-process
// secrets box: no call leaves the module.
func (s *server) newRSVPLink() (hash, sealed string, err error) {
	token, err := newRSVPToken()
	if err != nil {
		return "", "", err
	}
	sealed, err = s.deps.Secrets.SealString(rsvpLinkPurpose, s.rsvpBase()+"/rsvp?token="+token)
	if err != nil {
		return "", "", fmt.Errorf("calendar: seal an RSVP link: %w", err)
	}
	return tokenSHA256(token), sealed, nil
}

// rsvpLink is an outside attendee's link, opened from its sealed form.
func (s *server) rsvpLink(at store.CalendarAppointmentAttendee) (string, error) {
	if at.RsvpLinkCiphertext == nil {
		return "", fmt.Errorf("calendar: attendee %d has no RSVP link", at.ID)
	}
	link, err := s.deps.Secrets.OpenString(rsvpLinkPurpose, *at.RsvpLinkCiphertext)
	if err != nil {
		return "", fmt.Errorf("calendar: open attendee %d's RSVP link: %w", at.ID, err)
	}
	return link, nil
}

// attendeeOut is one attendee on the wire: a user named from the directory,
// a contact by the name the customer directory answers — never its address —
// an email attendee as typed, and an outside one's invitation record.
func attendeeOut(at store.CalendarAppointmentAttendee, ppl people) gen.AppointmentAttendee {
	out := gen.AppointmentAttendee{
		Id:          at.ID,
		Kind:        gen.AppointmentAttendeeKind(at.Kind),
		Status:      gen.AppointmentAttendeeStatus(at.Status),
		RespondedAt: utcPtr(at.RespondedAt),
	}
	switch at.Kind {
	case kindUser:
		if at.UserID != nil {
			out.User = &gen.CalendarUserRef{Id: *at.UserID, DisplayName: ppl.users[*at.UserID]}
		}
	case kindContact:
		if at.ContactID != nil {
			out.Contact = &gen.AppointmentAttendeeContact{Id: *at.ContactID, Name: contactName(ppl.contacts[*at.ContactID])}
		}
	case kindEmail:
		out.Email, out.Name = at.Email, at.Name
	}
	if isOutside(at) {
		out.Invitation = &gen.AttendeeInvitation{
			SentAt:   utcPtr(at.InvitedAt),
			Sequence: at.InvitedSequence,
			FailedAt: utcPtr(at.SendFailedAt),
		}
	}
	return out
}

// userAttendees is the user ids among atts, in order.
func userAttendees(atts []store.CalendarAppointmentAttendee) []uuid.UUID {
	var out []uuid.UUID
	for _, at := range atts {
		if at.Kind == kindUser && at.UserID != nil {
			out = append(out, *at.UserID)
		}
	}
	return out
}

// outsideAttendees is the outside attendees among atts, in order.
func outsideAttendees(atts []store.CalendarAppointmentAttendee) []store.CalendarAppointmentAttendee {
	var out []store.CalendarAppointmentAttendee
	for _, at := range atts {
		if isOutside(at) {
			out = append(out, at)
		}
	}
	return out
}
