package meta_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/metatest"
)

func TestGraphProfile_FetchesCachesAndExpires(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	ctx := context.Background()
	e.fake.AddUser(grPSID, "Kari Nordmann")

	p, err := e.m.Profile(ctx, grPageID, grPSID)
	if err != nil || p.DisplayName != "Kari Nordmann" {
		t.Fatalf("profile: %+v %v", p, err)
	}
	if n := len(e.fake.CallsTo("GET /" + grPSID)); n != 1 {
		t.Fatalf("%d calls", n)
	}

	e.fake.AddUser(grPSID, "Kari Hansen")
	e.h.Advance(29 * 24 * time.Hour)
	p, err = e.m.Profile(ctx, grPageID, grPSID)
	if err != nil || p.DisplayName != "Kari Nordmann" {
		t.Fatalf("cached profile: %+v %v", p, err)
	}
	if n := len(e.fake.CallsTo("GET /" + grPSID)); n != 1 {
		t.Fatalf("a cache hit called Meta: %d calls", n)
	}

	e.h.Advance(2 * 24 * time.Hour)
	p, err = e.m.Profile(ctx, grPageID, grPSID)
	if err != nil || p.DisplayName != "Kari Hansen" {
		t.Fatalf("expired profile: %+v %v", p, err)
	}
	if n := len(e.fake.CallsTo("GET /" + grPSID)); n != 2 {
		t.Fatalf("%d calls after expiry", n)
	}
}

func TestGraphProfile_FallsBackToFirstAndLastName(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	e.fake.AddUserParts(grPSID, "Ola", "Nordmann")
	p, err := e.m.Profile(context.Background(), grPageID, grPSID)
	if err != nil || p.DisplayName != "Ola Nordmann" {
		t.Fatalf("profile: %+v %v", p, err)
	}
}

func TestGraphProfile_AnEmptyNameIsNotCached(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	e.fake.AddUser(grPSID, "")
	p, err := e.m.Profile(context.Background(), grPageID, grPSID)
	if err != nil || p.DisplayName != "" {
		t.Fatalf("profile: %+v %v", p, err)
	}
	if n := e.h.Count(t, `SELECT count(*) FROM meta.profiles`); n != 0 {
		t.Fatalf("%d profiles cached", n)
	}
}

func TestGraphProfile_Refusals(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	ctx := context.Background()
	if _, err := e.m.Profile(ctx, "404", grPSID); !errors.Is(err, contracts.ErrMessengerEndpointUnknown) {
		t.Fatalf("unknown Page: %v", err)
	}
	if _, err := e.m.Profile(ctx, grPageID, "unknown"); err == nil {
		t.Fatal("an unknown person is not an error")
	}
	e.fake.AddUser(grPSID, "Kari")
	e.fake.FailNext("GET /"+grPSID, metatest.Failure{Network: true})
	if _, err := e.m.Profile(ctx, grPageID, grPSID); err == nil {
		t.Fatal("a network failure is not an error")
	}
	if n := e.h.Count(t, `SELECT count(*) FROM meta.profiles`); n != 0 {
		t.Fatalf("%d profiles cached after failures", n)
	}
}
