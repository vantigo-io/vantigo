//go:build entra

package entra_test

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The tenant test runs the module against a real Microsoft Entra tenant
// through the real guarded transport (Entra design D14, research §9):
//
//	go test -tags entra -run TestEntraTenant -v ./internal/entra/
//
// with the four variables of a test tenant — ENTRA_TEST_TENANT_ID and
// ENTRA_TEST_CLIENT_ID (the app registration's directory and application
// ids), ENTRA_TEST_CLIENT_SECRET (one of its client secrets, never logged)
// and ENTRA_TEST_DOMAIN (a verified domain of the tenant, onmicrosoft.com
// is fine, which the made-up UPN ends in). It skips without them, naming the
// missing one. The optional ENTRA_TEST_SYNCED_USER_ID and
// ENTRA_TEST_SYNCED_EMPLOYEE_NUMBER (the object id and the employee number
// of a user synchronised from an on-premises directory) add the last
// scenario, which settles what the module answers for such a user.
//
// The tenant needs (research §9):
//   - a dedicated tenant, never a production one: the test creates, enables,
//     updates, disables and deletes one user whose employee number starts
//     with VT- and whose family name starts with Test, and creates and
//     deletes one security group whose mail nickname starts with vt-test-;
//   - an app registration holding the eight application permissions, admin
//     consented: User.ReadWrite.All, User-LifeCycleInfo.ReadWrite.All,
//     User.RevokeSessions.All, UserAuthMethod-TAP.ReadWrite.All,
//     LicenseAssignment.ReadWrite.All, Group.ReadWrite.All, Domain.Read.All
//     and Organization.Read.All;
//   - the User Administrator role assigned to the application's service
//     principal (the permissions alone cannot disable, revoke or issue a pass
//     for a user, and the connection test cannot read the role);
//   - Temporary Access Pass enabled in the authentication methods policy for
//     all users (Entra admin centre, Protection, Authentication methods).
//
// The module runs on a throwaway database of the harness, so the secret is
// sealed with the harness's key and goes with that database; what the test
// makes in the tenant is removed in t.Cleanup if a step failed first (a
// disabled user is soft-deleted, restorable for thirty days from the portal's
// Deleted users; a group likewise from Deleted groups). The test logs ids,
// the UPN of the prefixed user and codes, never the secret or a pass. The
// module's own events, which carry the codes the tenant's answers became, are
// logged at the end.
//
// The group (delivery D): a prefixed security group made with the
// application's service principal — the token's oid, which the connection
// test records — and the test user as its owners, the user added as its
// member and removed again by $ref in a $batch, then deleted. A team is not
// attempted: it needs a Microsoft 365 group fifteen minutes old.

// tenantEnv reads the variable name, skipping the test when it is empty.
func tenantEnv(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Skipf("%s is not set; see the comment at the top of tenant_test.go", name)
	}
	return v
}

// randomToken is n characters from 0-9a-z, from the system's random source.
func randomToken(t *testing.T, n int) string {
	t.Helper()
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func TestEntraTenant(t *testing.T) {
	tenant := tenantEnv(t, "ENTRA_TEST_TENANT_ID")
	client := tenantEnv(t, "ENTRA_TEST_CLIENT_ID")
	secret := tenantEnv(t, "ENTRA_TEST_CLIENT_SECRET")
	domain := strings.ToLower(tenantEnv(t, "ENTRA_TEST_DOMAIN"))
	syncedID := strings.TrimSpace(os.Getenv("ENTRA_TEST_SYNCED_USER_ID"))
	syncedNumber := strings.TrimSpace(os.Getenv("ENTRA_TEST_SYNCED_EMPLOYEE_NUMBER"))

	// The real guarded transport: no modtest.WithTransport. The harness clock
	// is set to the wall clock, since the tenant's tokens and the pass's
	// lifetime are real.
	h := newHarness(t)
	h.Advance(time.Since(h.Now()))
	o, _ := owner(t, h)
	p := provider(h)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// The events are logged last (cleanups run in reverse), after the
	// cleanup below has removed what a failed run left.
	t.Cleanup(func() {
		r := o.Do(http.MethodGet, eventsPath+"?limit=200", nil)
		if r.Status != http.StatusOK {
			t.Logf("GET /events: status %d", r.Status)
			return
		}
		var events []gen.EntraEvent
		r.JSON(&events)
		for _, e := range slices.Backward(events) {
			upn := ""
			if e.Upn != nil {
				upn = " " + *e.Upn
			}
			t.Logf("event %s by %s%s codes=%v", e.Action, e.Actor, upn, e.Codes)
		}
	})

	// Steps 1 to 3: the tenant and the client, the secret, the test.
	if r := o.Do(http.MethodPut, connectionPath, map[string]any{"tenantId": tenant, "clientId": client}, ifMatch(getConnection(t, o).Version)); r.Status != http.StatusOK {
		t.Fatalf("PUT /connection: status %d body %s", r.Status, r.Body)
	}
	addSecret(t, o, secret, "Vantigo tenant test", h.Now().AddDate(1, 0, 0))
	result := testConnection(t, o)
	organisation := ""
	if result.OrganisationName != nil {
		organisation = *result.OrganisationName
	}
	reason := ""
	if result.Reason != nil {
		reason = *result.Reason
	}
	t.Logf("connection test: status=%s reason=%q organisation=%q verifiedDomains=%v roleKnown=%t", result.Status, reason, organisation, result.VerifiedDomains, result.RoleKnown)
	t.Logf("permissions present: %v", result.PermissionsPresent)
	for _, name := range result.PermissionsMissing {
		t.Errorf("the tenant lacks the permission %s: grant it to the application and give admin consent", name)
	}
	if result.Status != gen.EntraConnectionTestStatusConnected {
		t.Fatalf("the connection did not connect (%s): the rest of the test needs it", reason)
	}
	if !slices.Contains(result.VerifiedDomains, domain) {
		t.Fatalf("ENTRA_TEST_DOMAIN %q is not one of the tenant's verified domains %v", domain, result.VerifiedDomains)
	}

	// Step 4: the account rules.
	r := o.Do(http.MethodPut, settingsPath, map[string]any{
		"upnDomain":            domain,
		"writeHireDate":        true,
		"leaverStripLicences":  true,
		"leaverRemoveGroups":   "all",
		"leaverRevokeSessions": true,
		"leaverWriteLeaveDate": true,
		"passLifetimeMinutes":  60,
		"passUsableOnce":       true,
	}, ifMatch(getConnection(t, o).Version))
	if r.Status != http.StatusOK {
		t.Fatalf("PUT /connection/settings: status %d body %s", r.Status, r.Body)
	}

	// The lifecycle, through the provisioner as Staff's worker drives it.
	actor := contracts.Actor{System: contracts.SystemActorStaffAccounts}
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	token := randomToken(t, 6)
	spec := contracts.ExternalSpec{
		EmployeeNumber: "VT-" + strings.ToUpper(token),
		GivenName:      "Vantigo",
		FamilyName:     "Test" + token,
		WorkCountry:    "NO",
		HireDate:       &today,
		Fields:         map[string]string{"jobTitle": "Vantigo test"},
	}
	spec.DisplayName = spec.GivenName + " " + spec.FamilyName

	account, err := p.Prepare(ctx, actor, spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	id := account.ExternalID
	removed := false
	t.Cleanup(func() {
		if removed || id == "" {
			return
		}
		// A step failed first: disable and delete what was made, so no
		// enabled test user stays in the tenant.
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		leave := today
		leaver := spec
		leaver.LeaveDate = &leave
		if _, err := p.Deactivate(cleanup, actor, id, leaver); err != nil {
			t.Logf("cleanup: Deactivate %s: %v", id, err)
		}
		if err := p.Delete(cleanup, actor, id); err != nil {
			t.Logf("cleanup: Delete %s: %v", id, err)
			return
		}
		t.Logf("cleanup: deleted %s (%s)", id, account.UserPrincipalName)
	})
	t.Logf("Prepare: id=%s upn=%s state=%s created=%t reported=%v", id, account.UserPrincipalName, account.State, account.Created, account.Reported)
	if _, err := uuid.Parse(id); err != nil {
		t.Errorf("Prepare answered %q as the id, want the object id", id)
	}
	if !account.Created || account.State != "disabled" || !strings.HasPrefix(account.UserPrincipalName, "vantigo.test") || !strings.HasSuffix(account.UserPrincipalName, "@"+domain) {
		t.Errorf("Prepare: %+v, want a disabled user created with the made-up UPN ending @%s", account, domain)
	}

	read, err := p.Account(ctx, id)
	if err != nil || read == nil {
		t.Fatalf("Account after Prepare: %v %v", read, err)
	}
	if read.State != "disabled" {
		t.Errorf("Account after Prepare: state %s, want disabled", read.State)
	}

	// Activate, fresh: enabled, the hire date, a pass. The two uncertain
	// facts it settles are logged plainly.
	spec.Flags.Fresh = true
	account, err = p.Activate(ctx, actor, id, spec)
	if err != nil {
		t.Fatalf("Activate: %v (a 403 here, with the codes in the events below, means the application lacks the User Administrator role or the TAP permission UserAuthMethod-TAP.ReadWrite.All)", err)
	}
	t.Logf("Activate: state=%s hasPass=%t reported=%v", account.State, account.HasPass, account.Reported)
	if account.State != "enabled" {
		t.Errorf("Activate: state %s, want enabled", account.State)
	}
	if slices.Contains(account.Reported, contracts.ReportedHireDateNotWritten) {
		t.Logf("UNCERTAIN FACT 1: employeeHireDate was REFUSED with application permissions (%s reported): the hire date cannot be written app-only here", contracts.ReportedHireDateNotWritten)
	} else {
		t.Logf("UNCERTAIN FACT 1: employeeHireDate was WRITTEN with application permissions (User-LifeCycleInfo.ReadWrite.All)")
	}
	switch {
	case account.HasPass:
		t.Logf("UNCERTAIN FACT 2: a Temporary Access Pass was ISSUED with UserAuthMethod-TAP.ReadWrite.All")
	case slices.Contains(account.Reported, contracts.ReportedPassNotAllowed):
		t.Logf("UNCERTAIN FACT 2: the pass was NOT issued: the tenant's policy does not allow it (%s reported); enable Temporary Access Pass in the authentication methods policy", contracts.ReportedPassNotAllowed)
	case slices.Contains(account.Reported, "pass_forbidden"):
		t.Logf("UNCERTAIN FACT 2: the pass was NOT issued: the tenant answered 403 (pass_forbidden reported); the application lacks UserAuthMethod-TAP.ReadWrite.All or the role to issue one to this user")
	default:
		t.Errorf("UNCERTAIN FACT 2: no pass and neither %s nor pass_forbidden reported; reported=%v", contracts.ReportedPassNotAllowed, account.Reported)
	}

	if account.HasPass {
		pass, err := p.RevealPass(ctx, actor, id)
		if err != nil {
			t.Errorf("RevealPass: %v", err)
		} else {
			t.Logf("RevealPass: revealed once, %d characters, expires %s", len(pass.Value), pass.ExpiresAt.UTC().Format(time.RFC3339))
			if pass.Value == "" || !pass.ExpiresAt.After(time.Now()) {
				t.Errorf("RevealPass: an empty pass or one expired already (expires %s)", pass.ExpiresAt)
			}
		}
		if _, err := p.RevealPass(ctx, actor, id); !errors.Is(err, contracts.ErrPassGone) {
			t.Errorf("RevealPass a second time: %v, want ErrPassGone", err)
		}
	}

	// Update: the mover's attributes.
	spec.Flags.Fresh = false
	spec.Fields = map[string]string{"jobTitle": "Vantigo test, changed"}
	account, err = p.Update(ctx, actor, id, spec)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	t.Logf("Update: state=%s reported=%v", account.State, account.Reported)

	// The group: owners, members in a batch, deleted.
	if n := modtest.One[int](t, h.Harness, `SELECT count(*)::int FROM entra.connection WHERE service_principal_id IS NOT NULL`); n != 1 {
		t.Error("the connection test did not record the service principal (the token's oid claim)")
	}
	groupSpec := contracts.GroupSpec{
		GroupID: "vt-" + token, Key: "vt-test-" + token, Name: "Vantigo test " + token,
		Description: "Made by Vantigo's tenant test; safe to delete.",
		Values:      map[string]any{"type": "security"}, OwnerExternalIDs: []string{id},
	}
	group, err := p.EnsureGroup(ctx, actor, groupSpec)
	if err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	groupDeleted := false
	t.Cleanup(func() {
		if groupDeleted {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := p.DeleteGroup(cleanup, actor, groupSpec.GroupID); err != nil {
			t.Logf("cleanup: DeleteGroup %s: %v", group.ExternalID, err)
			return
		}
		t.Logf("cleanup: deleted the group %s", group.ExternalID)
	})
	t.Logf("EnsureGroup: id=%s created=%t pending=%v", group.ExternalID, group.Created, group.Pending)
	if !group.Created || len(group.Pending) != 0 {
		t.Errorf("EnsureGroup: %+v, want a group created with nothing pending", group)
	}
	if again, err := p.EnsureGroup(ctx, actor, groupSpec); err != nil || again.Created || again.ExternalID != group.ExternalID {
		t.Errorf("EnsureGroup again: %+v %v, want the same group", again, err)
	}
	members, err := p.SetMembers(ctx, actor, groupSpec.GroupID, []string{id})
	t.Logf("SetMembers (add): %+v %v", members, err)
	if err != nil || members.Added != 1 || len(members.Failed) != 0 {
		t.Errorf("SetMembers (add): %+v %v, want the user added (a 400 here can be the new group's few seconds of replication; run again)", members, err)
	}
	members, err = p.SetMembers(ctx, actor, groupSpec.GroupID, nil)
	t.Logf("SetMembers (remove): %+v %v", members, err)
	if err != nil || members.Removed != 1 || len(members.Failed) != 0 {
		t.Errorf("SetMembers (remove): %+v %v, want the user removed by $ref in a batch", members, err)
	}
	if read, err := p.Account(ctx, id); err != nil || read == nil {
		t.Errorf("Account after the member's removal: %+v %v — the user must still exist", read, err)
	}
	if err := p.DeleteGroup(ctx, actor, groupSpec.GroupID); err != nil {
		t.Errorf("DeleteGroup: %v", err)
	} else {
		groupDeleted = true
		t.Logf("DeleteGroup: %s soft-deleted (restorable for thirty days)", group.ExternalID)
	}

	// Deactivate: disabled, the leave date.
	spec.LeaveDate = &today
	account, err = p.Deactivate(ctx, actor, id, spec)
	if err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	t.Logf("Deactivate: state=%s reported=%v", account.State, account.Reported)
	if account.State != "disabled" {
		t.Errorf("Deactivate: state %s, want disabled", account.State)
	}
	if read, err := p.Account(ctx, id); err != nil || read == nil || read.State != "disabled" {
		t.Errorf("Account after Deactivate: %+v %v, want disabled", read, err)
	}

	// Delete: soft-deleted, the row kept and marked.
	if err := p.Delete(ctx, actor, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	removed = true
	t.Logf("Delete: %s soft-deleted (restorable for thirty days)", id)
	if state := modtest.One[string](t, h.Harness, `SELECT state FROM entra.accounts WHERE object_id = $1::uuid`, id); state != "deleted" {
		t.Errorf("the account's row is %q after Delete, want deleted", state)
	}
	if read, err := p.Account(ctx, id); err != nil || (read != nil && read.State != "deleted") {
		t.Errorf("Account after Delete: %+v %v, want nil or deleted", read, err)
	}

	// Uncertain fact 3: what a user synchronised from on-premises answers.
	// Nothing is written to it: Prepare finds it by its employee number, and
	// the module refuses before it changes anything.
	switch {
	case syncedID == "":
		t.Log("UNCERTAIN FACT 3: skipped, ENTRA_TEST_SYNCED_USER_ID is not set")
	case syncedNumber == "":
		t.Log("UNCERTAIN FACT 3: skipped, ENTRA_TEST_SYNCED_EMPLOYEE_NUMBER is not set (Prepare finds a user by its employee number)")
	default:
		synced, err := p.Prepare(ctx, actor, contracts.ExternalSpec{
			EmployeeNumber: syncedNumber, GivenName: "Vantigo", FamilyName: "Synced", WorkCountry: "NO",
		})
		switch {
		case errors.Is(err, contracts.ErrExternalSyncedOnPremises):
			t.Logf("UNCERTAIN FACT 3: Prepare of user %s answered ErrExternalSyncedOnPremises", syncedID)
		case err != nil:
			t.Logf("UNCERTAIN FACT 3: Prepare of user %s answered another error: %v", syncedID, err)
		default:
			t.Logf("UNCERTAIN FACT 3: Prepare of user %s answered no error: id=%s state=%s reported=%v", syncedID, synced.ExternalID, synced.State, synced.Reported)
		}
	}
}
