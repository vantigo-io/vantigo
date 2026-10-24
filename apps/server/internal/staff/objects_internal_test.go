package staff

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// The upload's bounds, pinned: its rate-limit policy and the router's body
// cap for it, the file itself plus the multipart framing — the only two
// entries the module registers.
func TestDocumentUpload_PolicyAndBodyLimit(t *testing.T) {
	want := ratelimit.Policy{
		Name:    "StaffDocumentUpload",
		Limit:   600,
		Window:  time.Hour,
		Message: "Too many document uploads. Please wait a little before adding more.",
	}
	if policyDocumentUpload != want {
		t.Errorf("policyDocumentUpload = %+v, want %+v", policyDocumentUpload, want)
	}
	if len(limits) != 6 || limits["postStaffPeopleByIdDocuments"] != policyDocumentUpload ||
		limits["postStaffPersonCompetencesByIdEvidence"] != policyDocumentUpload ||
		limits["postStaffAccountsRun"] != policyAccountsRun ||
		limits["postStaffPeopleByIdAccountRun"] != policyAccountAction || limits["postStaffPeopleByIdAccountDisable"] != policyAccountAction ||
		limits["postStaffPeopleByIdAccountPass"] != policyAccountAction {
		t.Errorf("limits = %v, want the uploads', the account runs' and the pass's policies alone", limits)
	}
	if policyAccountsRun.Limit != 30 || policyAccountsRun.Window != time.Hour ||
		policyAccountAction.Limit != 120 || policyAccountAction.Window != time.Hour {
		t.Errorf("the account runs' policies = %+v, %+v; want 30 and 120 an hour", policyAccountsRun, policyAccountAction)
	}
	if len(bodyLimits) != 3 || bodyLimits["postStaffPeopleByIdDocuments"] != 10*1024*1024+64*1024 ||
		bodyLimits["postStaffPersonCompetencesByIdEvidence"] != 10*1024*1024+64*1024 ||
		bodyLimits["postStaffPeopleImport"] != 5*1024*1024+64*1024 {
		t.Errorf("bodyLimits = %v, want the uploads' 10 MiB + 64 KiB and the import's 5 MiB + 64 KiB alone", bodyLimits)
	}
}

// A document's key is people/<personId>/<a fresh uuid>, never anything the
// client sent.
func TestDocumentObjectKey(t *testing.T) {
	a, b := documentObjectKey(1042), documentObjectKey(1042)
	pattern := regexp.MustCompile(`^people/1042/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !pattern.MatchString(a) || a == b {
		t.Errorf("keys %q and %q, want two distinct people/1042/<uuid>", a, b)
	}
}

// A store that cannot be built does not stop the module: the error is kept
// and every object call answers it, so a process — the API or a worker —
// starts without storage and fails only what needs it.
func TestObjectStore_ABrokenConfigurationIsDeferred(t *testing.T) {
	s := newServer(module.Deps{Config: &config.Config{StorageProvider: "s3"}})
	err := s.objectPut(context.Background(), "people/1/a", strings.NewReader("x"), "application/pdf")
	if err == nil || !strings.Contains(err.Error(), "s3") {
		t.Errorf("objectPut = %v, want the construction error", err)
	}
	if _, err := s.objectGet(context.Background(), "people/1/a"); err == nil {
		t.Error("objectGet answered no error over a broken store")
	}
	if err := s.objectDelete(context.Background(), "people/1/a"); err == nil {
		t.Error("objectDelete answered no error over a broken store")
	}
}
