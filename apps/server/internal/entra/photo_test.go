package entra_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

// The account's photo (staff photo sync, spec §4 "Staff → Entra"): SetPhoto
// converts the picture and PUTs it to /users/{id}/photo/$value as a JPEG;
// a picture the tenant refuses, or that cannot be decoded, answers nil and
// the account reports photo_not_written until a photo is written or
// cleared; ClearPhoto DELETEs it, a user with none being done.

const photoPath = "/photo/$value"

// pngPhoto is a w×h PNG of one colour, as Staff holds it.
func pngPhoto(t *testing.T, w, h int) contracts.Photo {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: 200, G: 120, B: 40, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return contracts.Photo{ContentType: "image/png", Data: b.Bytes()}
}

// photoAccount is a user the module created and enabled, which it may
// write to.
func photoAccount(t *testing.T, l *lifecycle) string {
	t.Helper()
	spec := joiner("E-1")
	id := l.prepare(t, spec).ExternalID
	l.activate(t, id, spec)
	return id
}

// reported is the codes Account answers for id.
func (l *lifecycle) reported(t *testing.T, id string) []string {
	t.Helper()
	acc, err := l.p.Account(context.Background(), id)
	if err != nil || acc == nil {
		t.Fatalf("Account: %+v %v", acc, err)
	}
	return acc.Reported
}

func TestSetPhoto_WritesAPNGAsAJPEGOf1024(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	id := photoAccount(t, l)
	mark := len(l.g.Calls())
	photo := pngPhoto(t, 3000, 2000)

	if err := l.p.SetPhoto(ctx, staffActor, id, photo); err != nil {
		t.Fatalf("SetPhoto: %v", err)
	}
	u := l.user(t, id)
	if u.PhotoContentType != "image/jpeg" || len(u.Photo) == 0 || len(u.Photo) > 4_000_000 {
		t.Fatalf("the tenant's photo: %s, %d bytes", u.PhotoContentType, len(u.Photo))
	}
	img, err := jpeg.Decode(bytes.NewReader(u.Photo))
	if err != nil {
		t.Fatalf("the tenant's photo is not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 1024 || b.Dy() != 682 {
		t.Errorf("the tenant's photo is %d×%d, want 1024×682", b.Dx(), b.Dy())
	}
	if events := l.events(t, "entra.user.photo_set"); len(events) != 1 || !slices.Equal(events[0], []string{"photo_written"}) {
		t.Errorf("the photo_set events: %v, want one photo_written", events)
	}
	if got := l.reported(t, id); hasCode(got, contracts.ReportedPhotoNotWritten) {
		t.Errorf("a written photo is reported: %v", got)
	}

	// The same photo again is written again: the caller decides when.
	if err := l.p.SetPhoto(ctx, staffActor, id, photo); err != nil {
		t.Fatalf("SetPhoto again: %v", err)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPut, photoPath); n != 2 {
		t.Errorf("%d PUTs of the photo, want 2", n)
	}
	if w := writes(l.graphCalls(mark)); !slices.Equal(w, []string{"PUT $value", "PUT $value"}) {
		t.Errorf("the writes: %v, want the two PUTs alone", w)
	}
}

func TestSetPhoto_ARefusedPhotoIsReportedUntilOneIsWritten(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	id := photoAccount(t, l)
	l.h.Exec(t, `UPDATE entra.accounts SET reported = ARRAY['manager_not_found'] WHERE object_id = $1`, id)

	// The tenant refuses it: nil, reported, and the other codes kept.
	l.g.Configure(func(g *graphtest.FakeGraph) { g.PhotoDenied = true })
	if err := l.p.SetPhoto(ctx, staffActor, id, pngPhoto(t, 300, 300)); err != nil {
		t.Fatalf("SetPhoto refused by the tenant: %v, want nil", err)
	}
	if got := l.reported(t, id); !slices.Equal(got, []string{"manager_not_found", contracts.ReportedPhotoNotWritten}) {
		t.Errorf("the reported codes after a refusal: %v", got)
	}
	if events := l.events(t, "entra.user.photo_set"); len(events) != 1 || !hasCode(events[0], "refused:photo_not_written") {
		t.Errorf("the photo_set events: %v, want refused:photo_not_written", events)
	}
	if u := l.user(t, id); u.Photo != nil {
		t.Error("the refused photo is in the tenant")
	}

	// A picture that cannot be decoded: reported, nothing sent.
	l.g.Configure(func(g *graphtest.FakeGraph) { g.PhotoDenied = false })
	mark := len(l.g.Calls())
	if err := l.p.SetPhoto(ctx, staffActor, id, contracts.Photo{ContentType: "image/jpeg", Data: []byte("not a picture")}); err != nil {
		t.Fatalf("SetPhoto of garbage: %v, want nil", err)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodPut, photoPath); n != 0 {
		t.Errorf("garbage was sent: %d PUTs", n)
	}
	if got := l.reported(t, id); !hasCode(got, contracts.ReportedPhotoNotWritten) {
		t.Errorf("the reported codes after garbage: %v", got)
	}

	// A photo written clears the code, and the others stay.
	if err := l.p.SetPhoto(ctx, staffActor, id, pngPhoto(t, 300, 300)); err != nil {
		t.Fatalf("SetPhoto: %v", err)
	}
	if got := l.reported(t, id); !slices.Equal(got, []string{"manager_not_found"}) {
		t.Errorf("the reported codes after a photo written: %v", got)
	}
	if u := l.user(t, id); u.Photo == nil {
		t.Error("the photo is not in the tenant")
	}
}

func TestClearPhoto_RemovesThePhotoAndIsIdempotent(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	id := photoAccount(t, l)

	if err := l.p.SetPhoto(ctx, staffActor, id, pngPhoto(t, 100, 100)); err != nil {
		t.Fatalf("SetPhoto: %v", err)
	}
	if err := l.p.ClearPhoto(ctx, staffActor, id); err != nil {
		t.Fatalf("ClearPhoto: %v", err)
	}
	if u := l.user(t, id); u.Photo != nil {
		t.Error("the photo is still in the tenant")
	}

	// None left (404), and a refusal reported before: done, the code gone.
	l.h.Exec(t, `UPDATE entra.accounts SET reported = ARRAY['photo_not_written'] WHERE object_id = $1`, id)
	mark := len(l.g.Calls())
	if err := l.p.ClearPhoto(ctx, staffActor, id); err != nil {
		t.Fatalf("ClearPhoto with no photo: %v, want nil", err)
	}
	if n := countCalls(l.graphCalls(mark), http.MethodDelete, photoPath); n != 1 {
		t.Errorf("%d DELETEs of the photo, want 1", n)
	}
	if got := l.reported(t, id); hasCode(got, contracts.ReportedPhotoNotWritten) {
		t.Errorf("the reported codes after a clear: %v", got)
	}
	if events := l.events(t, "entra.user.photo_cleared"); len(events) != 2 || !slices.Equal(events[1], []string{"photo_cleared"}) {
		t.Errorf("the photo_cleared events: %v, want two photo_cleared", events)
	}
}

func TestPhoto_AnUnconfirmedAdoptionIsRefused(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	seeded := l.g.AddUser(graphtest.User{
		DisplayName: "Kari Nordmann", MailNickname: "kari", UserPrincipalName: "kari@nordlys.no", EmployeeID: "E-4",
	})
	if _, err := l.p.Prepare(ctx, staffActor, joiner("E-4")); !errors.Is(err, contracts.ErrExternalUnconfirmed) {
		t.Fatalf("Prepare of the found user: %v, want ErrExternalUnconfirmed", err)
	}
	mark := len(l.g.Calls())
	if err := l.p.SetPhoto(ctx, staffActor, seeded.ID, pngPhoto(t, 100, 100)); !errors.Is(err, contracts.ErrExternalUnconfirmed) {
		t.Errorf("SetPhoto of an unconfirmed adoption: %v, want ErrExternalUnconfirmed", err)
	}
	if err := l.p.ClearPhoto(ctx, staffActor, seeded.ID); !errors.Is(err, contracts.ErrExternalUnconfirmed) {
		t.Errorf("ClearPhoto of an unconfirmed adoption: %v, want ErrExternalUnconfirmed", err)
	}
	if w := writes(l.graphCalls(mark)); len(w) != 0 {
		t.Errorf("writes to an unconfirmed adoption: %v", w)
	}
	for _, action := range []string{"entra.user.photo_set", "entra.user.photo_cleared"} {
		if events := l.events(t, action); len(events) != 1 || !hasCode(events[0], "refused:adoption_unconfirmed") {
			t.Errorf("the %s events: %v, want refused:adoption_unconfirmed", action, events)
		}
	}
}

func TestPhoto_NoConnectionAndNoActorAreRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, g := newTenantHarness(t)
	p := provider(h)
	id := "6a1c3b52-0f1e-4c8d-9a7b-2e3f4a5b6c7d"
	if err := p.SetPhoto(ctx, staffActor, id, pngPhoto(t, 10, 10)); !errors.Is(err, contracts.ErrProviderUnavailable) {
		t.Errorf("SetPhoto unconfigured: %v, want ErrProviderUnavailable", err)
	}
	if err := p.ClearPhoto(ctx, staffActor, id); !errors.Is(err, contracts.ErrProviderUnavailable) {
		t.Errorf("ClearPhoto unconfigured: %v, want ErrProviderUnavailable", err)
	}
	if n := len(g.Calls()); n != 0 {
		t.Errorf("an unconfigured provider made %d calls", n)
	}

	l := newLifecycle(t)
	id = photoAccount(t, l)
	mark := len(l.g.Calls())
	if err := l.p.SetPhoto(ctx, contracts.Actor{}, id, pngPhoto(t, 10, 10)); !errors.Is(err, contracts.ErrInvalidActor) {
		t.Errorf("SetPhoto by nobody: %v, want ErrInvalidActor", err)
	}
	if err := l.p.ClearPhoto(ctx, contracts.Actor{}, id); !errors.Is(err, contracts.ErrInvalidActor) {
		t.Errorf("ClearPhoto by nobody: %v, want ErrInvalidActor", err)
	}
	l.h.Exec(t, `UPDATE entra.connection SET status = 'failing', status_reason = 'tenant_unreachable'`)
	if err := l.p.SetPhoto(ctx, staffActor, id, pngPhoto(t, 10, 10)); !errors.Is(err, contracts.ErrProviderUnavailable) {
		t.Errorf("SetPhoto failing: %v, want ErrProviderUnavailable", err)
	}
	if err := l.p.ClearPhoto(ctx, staffActor, id); !errors.Is(err, contracts.ErrProviderUnavailable) {
		t.Errorf("ClearPhoto failing: %v, want ErrProviderUnavailable", err)
	}
	if n := len(l.g.Calls()) - mark; n != 0 {
		t.Errorf("refused photo calls made %d calls", n)
	}
}

// failPhotoPutWith makes the fake answer the next PUT of id's photo by
// queuing fn once the user's read before it is answered.
func failPhotoPutWith(l *lifecycle, id string, fn func(g *graphtest.FakeGraph)) {
	var once sync.Once
	l.w.afterOn(http.MethodGet+" /users/"+id, func() { once.Do(func() { fn(l.g) }) })
}

func TestSetPhoto_AThrottleIsWaitedOnce(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	id := photoAccount(t, l)
	mark := len(l.g.Calls())
	failPhotoPutWith(l, id, func(g *graphtest.FakeGraph) { g.Throttle(1) })
	if err := l.p.SetPhoto(context.Background(), staffActor, id, pngPhoto(t, 100, 100)); err != nil {
		t.Fatalf("SetPhoto throttled once: %v", err)
	}
	var statuses []int
	for _, c := range l.graphCalls(mark) {
		if c.Method == http.MethodPut {
			statuses = append(statuses, c.Status)
		}
	}
	if !slices.Equal(statuses, []int{http.StatusTooManyRequests, http.StatusOK}) {
		t.Errorf("the PUTs answered %v, want 429 then 200", statuses)
	}
	if u := l.user(t, id); u.Photo == nil {
		t.Error("the photo is not in the tenant")
	}
}

func TestSetPhoto_AFailureIsReturnedNotReported(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	id := photoAccount(t, l)
	failPhotoPutWith(l, id, func(g *graphtest.FakeGraph) {
		g.FailNext(http.StatusInternalServerError)
		g.FailNext(http.StatusInternalServerError)
	})
	err := l.p.SetPhoto(context.Background(), staffActor, id, pngPhoto(t, 100, 100))
	if err == nil || errors.Is(err, contracts.ErrProviderUnavailable) {
		t.Fatalf("SetPhoto answered 500 twice: %v, want a failure to retry", err)
	}
	if got := l.reported(t, id); hasCode(got, contracts.ReportedPhotoNotWritten) {
		t.Errorf("a failure is reported: %v", got)
	}
	if events := l.events(t, "entra.user.photo_set"); len(events) != 0 {
		t.Errorf("a failure was recorded: %v", events)
	}
}

func TestSetPhoto_A404OnThePutIsARefusal(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	id := photoAccount(t, l)
	failPhotoPutWith(l, id, func(g *graphtest.FakeGraph) { g.FailNext(http.StatusNotFound) })
	if err := l.p.SetPhoto(context.Background(), staffActor, id, pngPhoto(t, 100, 100)); err != nil {
		t.Fatalf("SetPhoto answered 404: %v, want nil", err)
	}
	if got := l.reported(t, id); !hasCode(got, contracts.ReportedPhotoNotWritten) {
		t.Errorf("the reported codes after a 404: %v", got)
	}
	if events := l.events(t, "entra.user.photo_set"); len(events) != 1 || !hasCode(events[0], "refused:photo_not_written") {
		t.Errorf("the photo_set events: %v, want refused:photo_not_written", events)
	}
}

func TestClearPhoto_ARefusedClearIsReported(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	ctx := context.Background()
	id := photoAccount(t, l)
	if err := l.p.SetPhoto(ctx, staffActor, id, pngPhoto(t, 100, 100)); err != nil {
		t.Fatalf("SetPhoto: %v", err)
	}
	l.h.Exec(t, `UPDATE entra.accounts SET reported = ARRAY['manager_not_found'] WHERE object_id = $1`, id)

	l.g.Configure(func(g *graphtest.FakeGraph) { g.PhotoDenied = true })
	if err := l.p.ClearPhoto(ctx, staffActor, id); err != nil {
		t.Fatalf("ClearPhoto refused by the tenant: %v, want nil", err)
	}
	if got := l.reported(t, id); !slices.Equal(got, []string{"manager_not_found", contracts.ReportedPhotoNotWritten}) {
		t.Errorf("the reported codes after a refused clear: %v", got)
	}
	if events := l.events(t, "entra.user.photo_cleared"); len(events) != 1 || !hasCode(events[0], "refused:photo_not_written") {
		t.Errorf("the photo_cleared events: %v, want refused:photo_not_written", events)
	}
	if u := l.user(t, id); u.Photo == nil {
		t.Error("the refused clear removed the photo")
	}

	// Allowed again: cleared, the code gone and the others kept.
	l.g.Configure(func(g *graphtest.FakeGraph) { g.PhotoDenied = false })
	if err := l.p.ClearPhoto(ctx, staffActor, id); err != nil {
		t.Fatalf("ClearPhoto: %v", err)
	}
	if got := l.reported(t, id); !slices.Equal(got, []string{"manager_not_found"}) {
		t.Errorf("the reported codes after a clear: %v", got)
	}
}
