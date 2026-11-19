package identity_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// contracts.Brands (brands design B29) is what Customers, Invoices and Orders
// read: the published brands for a picker, one brand's published version, and a
// logo by its hash. It never returns a draft, a pending draft version, and — a
// campaign being a draft-free overlay applied at resolution time only — never a
// campaign look.
func TestBrandsContract_ReadsOnlyWhatIsPublished(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withBrandStorage(t))
	owner, _ := h.bootstrapOwner(t)
	brands := identity.Module(h.access).Brands(h.deps)
	ctx := context.Background()

	// The default brand, published with a mark.
	def := getBrand(t, owner)
	withLogo := putLogoBody(t, owner, def, "mark_light", pngOf(t, 64, 32))
	sha := withLogo.Draft.Logos["mark_light"].Sha256

	// A logo only a draft names is not served by the contract.
	if _, err := brands.LogoBytes(ctx, sha); !errors.Is(err, contracts.ErrBrandLogoNotFound) {
		t.Fatalf("LogoBytes of a draft's logo: %v, want ErrBrandLogoNotFound", err)
	}
	if r := publish(owner, withLogo); r.status != http.StatusOK {
		t.Fatalf("publish the default brand: %d %s", r.status, r.body)
	}
	logo, err := brands.LogoBytes(ctx, sha)
	if err != nil || logo.ContentType != "image/png" || !bytes.HasPrefix(logo.Bytes, []byte("\x89PNG")) {
		t.Fatalf("LogoBytes of a published logo = %q (%d bytes), %v", logo.ContentType, len(logo.Bytes), err)
	}
	if _, err := brands.LogoBytes(ctx, strings.Repeat("0", 64)); !errors.Is(err, contracts.ErrBrandLogoNotFound) {
		t.Errorf("LogoBytes of an unknown hash: %v, want ErrBrandLogoNotFound", err)
	}
	if _, err := brands.LogoBytes(ctx, "../etc/passwd"); !errors.Is(err, contracts.ErrBrandLogoNotFound) {
		t.Errorf("LogoBytes of a malformed hash: %v, want ErrBrandLogoNotFound", err)
	}

	// A brand still a draft is invisible; Get answers (nil, nil) for it.
	draft := createBrand(t, owner, "Pending", tealPrimary)
	list, err := brands.List(ctx)
	if err != nil || len(list) != 1 || !list[0].Default {
		t.Fatalf("List with a draft brand = %+v, %v, want the default alone", list, err)
	}
	if got, err := brands.Get(ctx, uuid.MustParse(draft.ID)); err != nil || got != nil {
		t.Errorf("Get of a draft brand = %+v, %v, want nil", got, err)
	}
	if got, err := brands.Get(ctx, uuid.New()); err != nil || got != nil {
		t.Errorf("Get of an unknown id = %+v, %v, want nil", got, err)
	}

	// Published, it is listed by name after the default, with its published look.
	pub := publishedBrand(t, owner, "Nordlys", tealPrimary)
	list, err = brands.List(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "Vantigo" || !list[0].Default || list[1].Name != "Nordlys" {
		t.Fatalf("List = %+v, %v, want the default then Nordlys", list, err)
	}
	if list[1].Primary != tealPrimary || list[1].State != contracts.BrandStatePublished || list[1].Title != "Nordlys" {
		t.Errorf("Nordlys = %+v", list[1])
	}
	if got := list[0].Logos["mark_light"]; got.SHA256 != sha || got.ContentType != "image/png" {
		t.Errorf("the default's mark_light = %+v, want the published logo", got)
	}

	// A pending draft version of a published brand is never read.
	edit := saveDraft(owner, pub, inputs("Nordlys Renamed", "#b45309"))
	if edit.status != http.StatusOK {
		t.Fatalf("save a draft: %d %s", edit.status, edit.body)
	}
	got, err := brands.Get(ctx, uuid.MustParse(pub.ID))
	if err != nil || got == nil || got.Name != "Nordlys" || got.Primary != tealPrimary {
		t.Fatalf("Get with a pending draft = %+v, %v, want the published version", got, err)
	}

	// Archived: out of the picker, still resolvable and marked, its published
	// logos still readable for a document that copies them.
	fresh := brandByID(t, owner, pub.ID)
	if r := owner.do(http.MethodPost, fmt.Sprintf("%s/%s/archive", brandsPath, pub.ID), nil, header("If-Match", fmt.Sprint(fresh.Revision))); r.status != http.StatusOK {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	list, err = brands.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List after an archive = %+v, %v, want the default alone", list, err)
	}
	got, err = brands.Get(ctx, uuid.MustParse(pub.ID))
	if err != nil || got == nil || got.State != contracts.BrandStateArchived || got.Name != "Nordlys" {
		t.Fatalf("Get of an archived brand = %+v, %v, want it marked archived", got, err)
	}
}

func putLogoBody(t *testing.T, c *client, b brandBody, slot string, data []byte) brandBody {
	t.Helper()
	r := putLogo(t, c, b, slot, data)
	if r.status != http.StatusOK {
		t.Fatalf("PUT logo: %d %s", r.status, r.body)
	}
	var out brandBody
	r.json(&out)
	return out
}

func brandByID(t *testing.T, c *client, id string) brandBody {
	t.Helper()
	r := c.do(http.MethodGet, brandsPath+"/"+id, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET brand: %d %s", r.status, r.body)
	}
	var b brandBody
	r.json(&b)
	return b
}
