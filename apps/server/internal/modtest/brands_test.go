package modtest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The fake answers as the contract promises: List the published brands with the
// default first, Get any brand it holds, LogoBytes only a logo it was given.
func TestFakeBrands_AnswersAsTheContractPromises(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	def, plain, old := uuid.New(), uuid.New(), uuid.New()
	f := NewFakeBrands()
	f.Set(
		contracts.BrandEntry{ID: plain, Name: "Plain"},
		contracts.BrandEntry{ID: def, Name: "Default", Default: true},
		contracts.BrandEntry{ID: old, Name: "Old", State: contracts.BrandStateArchived},
	)
	list, err := f.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != def || list[1].ID != plain {
		t.Fatalf("List = %+v, %v, want the default then Plain, without the archived one", list, err)
	}
	if got, _ := f.Get(ctx, old); got == nil || got.State != contracts.BrandStateArchived {
		t.Errorf("Get of an archived brand = %+v, want it", got)
	}
	if got, _ := f.Get(ctx, uuid.New()); got != nil {
		t.Errorf("Get of an unknown id = %+v, want nil", got)
	}
	if _, err := f.LogoBytes(ctx, "abc"); !errors.Is(err, contracts.ErrBrandLogoNotFound) {
		t.Errorf("LogoBytes without a logo: %v, want ErrBrandLogoNotFound", err)
	}
	f.SetLogo("abc", contracts.BrandLogo{Bytes: []byte("x"), ContentType: "image/png"})
	if l, err := f.LogoBytes(ctx, "abc"); err != nil || string(l.Bytes) != "x" {
		t.Errorf("LogoBytes = %+v, %v", l, err)
	}
	boom := errors.New("boom")
	f.SetError(boom)
	if _, err := f.List(ctx); !errors.Is(err, boom) {
		t.Errorf("List with an error set: %v", err)
	}
}
