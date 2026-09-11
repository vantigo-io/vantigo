package module

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The permission catalog as Compose composes it, ported from .NET's
// AuthorizationCatalogTests: every module's permissions whatever the module
// order, each validated as it is contributed, and every key an operation
// requires present before the module's router mounts it.

// catalogContract is a one-operation contract for a module named name, so
// the catalog fixtures compose under the real module names their keys'
// prefixes must match.
func catalogContract(name string) string {
	return `
openapi: 3.0.3
info: { title: ` + name + `, version: "1" }
paths:
  /api/v1/` + name + `/x:
    get:
      operationId: get` + name + `X
      x-vantigo-access: session
      responses:
        "204": { description: ok }
`
}

var catalogDocs = map[string]string{
	"identity":  catalogContract("identity"),
	"customers": catalogContract("customers"),
	"energy":    catalogContract("energy"),
}

// catalogPermission is .NET's test descriptor for key
// (AuthorizationCatalogTests.cs:13-14).
func catalogPermission(key string) contracts.Permission {
	return contracts.Permission{Key: key, Display: key, Description: "Description for " + key, Category: "Tests", Delegable: true}
}

// catalogModule is a module named name contributing the one permission key,
// whose Mount records the catalog it was given.
func catalogModule(name, key string, seen *[]map[string]contracts.Permission) Module {
	return Module{
		Name:        name,
		Permissions: []contracts.Permission{catalogPermission(key)},
		Mount: func(d Deps) (http.Handler, error) {
			*seen = append(*seen, d.Catalog)
			return http.NotFoundHandler(), nil
		},
	}
}

// Ported from AuthorizationCatalogTests.CatalogResolutionIsDeferredUntilAllContributorsAreRegistered.
// .NET resolved the catalog only once every contributor had registered.
// Compose builds it before it mounts any module, so identity, composed
// first, is mounted with the permission a module after it contributes.
func TestCompose_CatalogHoldsThePermissionsOfModulesComposedLater(t *testing.T) {
	var seen []map[string]contracts.Permission
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(catalogDocs),
		catalogModule("identity", "identity:manage", &seen),
		catalogModule("customers", "customers:view", &seen),
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got := slices.Sorted(maps.Keys(seen[0])); !slices.Equal(got, []string{"customers:view", "identity:manage"}) {
		t.Errorf("identity's Mount saw the catalog %v, want customers:view and identity:manage", got)
	}
}

// Ported from AuthorizationCatalogTests.ContributorsMayBeRegisteredBeforeFinalizationAndDoubleFinalizationThrows.
// Modules may come before or after identity: every order composes the same
// catalog, and every module is mounted with all of it. The listing order
// .NET asserted is the key order GET /access/catalog serves
// (TestAccessCatalog_ListsEveryModulesPermissionsInKeyOrder). The test's
// second half, a second AddPermissionCatalog throwing, has no counterpart:
// the catalog is a value of one Compose call, with no finalization step to
// repeat, and a key contributed twice is refused instead
// (TestCompose_DuplicateAndMalformedPermissionsFail).
func TestCompose_CatalogIsTheSameInEveryModuleOrder(t *testing.T) {
	keys := map[string]string{"customers": "customers:view", "energy": "energy:view", "identity": "identity:manage"}
	for _, order := range [][]string{
		{"customers", "energy", "identity"},
		{"identity", "customers", "energy"},
		{"energy", "identity", "customers"},
	} {
		var seen []map[string]contracts.Permission
		var mods []Module
		for _, name := range order {
			mods = append(mods, catalogModule(name, keys[name], &seen))
		}
		if _, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(catalogDocs), mods...); err != nil {
			t.Fatalf("compose %v: %v", order, err)
		}
		if len(seen) != 3 {
			t.Fatalf("order %v: %d mounts, want 3", order, len(seen))
		}
		for i, catalog := range seen {
			if got := slices.Sorted(maps.Keys(catalog)); !slices.Equal(got, []string{"customers:view", "energy:view", "identity:manage"}) {
				t.Errorf("order %v: mount %d saw %v", order, i, got)
			}
			if catalog["energy:view"] != catalogPermission("energy:view") {
				t.Errorf("order %v: mount %d saw energy:view as %+v", order, i, catalog["energy:view"])
			}
		}
	}
}

// Ported from AuthorizationCatalogTests.DuplicateAndMalformedCatalogEntriesFailValidation.
// .NET collected both problems into one exception; Compose stops at the
// first, so each is composed on its own and must fail naming its problem.
// A key cannot be registered by two different modules at all: its prefix
// must be the contributing module's name.
func TestCompose_DuplicateAndMalformedPermissionsFail(t *testing.T) {
	cases := []struct {
		name string
		mod  Module
		want string
	}{
		{
			"a key registered more than once",
			Module{Name: "identity", Permissions: []contracts.Permission{catalogPermission("identity:manage"), catalogPermission("identity:manage")}, Mount: staticHandler("identity")},
			`duplicate permission "identity:manage"`,
		},
		{
			"a malformed key",
			Module{Name: "customers", Permissions: []contracts.Permission{catalogPermission("Customers:View")}, Mount: staticHandler("customers")},
			`"Customers:View" is not of the form module:verb`,
		},
		{
			"a key another module owns",
			Module{Name: "customers", Permissions: []contracts.Permission{catalogPermission("identity:manage")}, Mount: staticHandler("customers")},
			`"identity:manage" does not start with module "customers"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(catalogDocs), c.mod)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("compose: %v, want an error containing %s", err, c.want)
			}
		})
	}
}

// permissionRoutesContract is an identity contract with one operation that
// requires identity:manage and, when missing is set, one that requires
// customers:view.
func permissionRoutesContract(missing bool) string {
	doc := `
openapi: 3.0.3
info: { title: Identity, version: "1" }
paths:
  /api/v1/identity/registered:
    get:
      operationId: getRegistered
      x-vantigo-access: permission:identity:manage
      responses:
        "204": { description: ok }
`
	if missing {
		doc += `
  /api/v1/identity/missing:
    get:
      operationId: getMissing
      x-vantigo-access: permission:customers:view
      responses:
        "204": { description: ok }
`
	}
	return doc
}

// routedIdentity is identity contributing identity:manage, with a Mount that
// registers every operation of its contract on a module Router over the
// composed catalog, as a module's generated server does, and fails with the
// router's Err.
func routedIdentity() Module {
	return Module{
		Name:        "identity",
		Permissions: []contracts.Permission{catalogPermission("identity:manage")},
		Mount: func(d Deps) (http.Handler, error) {
			router := NewRouter(RouterOptions{Doc: d.Doc, Access: d.Access, Catalog: d.Catalog})
			for path := range d.Doc.Paths.Map() {
				router.HandleFunc(http.MethodGet+" "+path, noopHandler)
			}
			return router, router.Err()
		},
	}
}

// Ported from AuthorizationCatalogTests.EndpointPermissionMetadataMustBeRegisteredAtStartup.
// Startup fails when an operation requires a key no module contributed, and
// the failure names that key.
func TestCompose_AnOperationsPermissionMustBeInTheCatalog(t *testing.T) {
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"identity": permissionRoutesContract(true)}), routedIdentity())
	if err == nil || !strings.Contains(err.Error(), `permission "customers:view" is not in the catalog`) {
		t.Fatalf("compose: %v, want the missing customers:view named", err)
	}
	if strings.Contains(err.Error(), "getRegistered") {
		t.Errorf("compose: %v, which also reports the operation whose key is registered", err)
	}
}

// Ported from AuthorizationCatalogTests.ValidEndpointPermissionIsAcceptedByStartupValidation.
func TestCompose_AnOperationWhosePermissionIsInTheCatalogMounts(t *testing.T) {
	if _, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"identity": permissionRoutesContract(false)}), routedIdentity()); err != nil {
		t.Fatalf("compose: %v", err)
	}
}
