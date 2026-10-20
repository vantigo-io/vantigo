package entra

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The provider's key — the identity_links.provider value Staff records — and
// its display name.
const (
	providerKey  = "entra"
	providerName = "Microsoft Entra ID"
)

// provisioner is the module's contracts.IdentityProvisioner (Entra design D6,
// D13): Staff's decisions executed against the tenant. Until tasks 3 and 4
// build it, it names itself unconfigured and writes nothing — every write
// answers ErrProviderUnavailable, which Staff records and retries — and the
// groups, delivery D's, answer ErrGroupNotManageable.
type provisioner struct {
	deps module.Deps
}

var _ contracts.IdentityProvisioner = (*provisioner)(nil)

// newProvisioner builds the module's provider over d, for Compose and Workers
// alike.
func newProvisioner(d module.Deps) contracts.IdentityProvisioner {
	return &provisioner{deps: d}
}

// Key is "entra", with no context and no network.
func (p *provisioner) Key() string { return providerKey }

// Provider names the provider and its status.
func (p *provisioner) Provider(context.Context) (contracts.ProviderInfo, error) {
	// Task 3 reads the status from the connection's row (status.go).
	return contracts.ProviderInfo{Key: providerKey, Name: providerName, Status: contracts.ProviderUnconfigured}, nil
}

// Prepare finds or creates the person's user, disabled.
func (p *provisioner) Prepare(context.Context, contracts.Actor, contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	// Task 4 implements this.
	return contracts.ExternalAccount{}, contracts.ErrProviderUnavailable
}

// Activate enables the user on the day.
func (p *provisioner) Activate(context.Context, contracts.Actor, string, contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	// Task 4 implements this.
	return contracts.ExternalAccount{}, contracts.ErrProviderUnavailable
}

// Deactivate disables the user and runs the leaver's steps.
func (p *provisioner) Deactivate(context.Context, contracts.Actor, string, contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	// Task 4 implements this.
	return contracts.ExternalAccount{}, contracts.ErrProviderUnavailable
}

// Update writes the mapped attributes and the manager.
func (p *provisioner) Update(context.Context, contracts.Actor, string, contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	// Task 4 implements this.
	return contracts.ExternalAccount{}, contracts.ErrProviderUnavailable
}

// Delete soft-deletes a disabled user.
func (p *provisioner) Delete(context.Context, contracts.Actor, string) error {
	// Task 4 implements this.
	return contracts.ErrProviderUnavailable
}

// Account reads the user.
func (p *provisioner) Account(context.Context, string) (*contracts.ExternalAccount, error) {
	// Task 4 implements this.
	return nil, contracts.ErrProviderUnavailable
}

// RevealPass answers the user's pass once.
func (p *provisioner) RevealPass(context.Context, contracts.Actor, string) (contracts.Pass, error) {
	// Task 4 implements this.
	return contracts.Pass{}, contracts.ErrProviderUnavailable
}

// GroupTraits declares the traits a group may carry for Entra.
func (p *provisioner) GroupTraits() []contracts.GroupTrait {
	// Task 4 declares type and mailNickname.
	return nil
}

// EnsureGroup is delivery D's.
func (p *provisioner) EnsureGroup(context.Context, contracts.Actor, contracts.GroupSpec) (contracts.ExternalGroup, error) {
	// Task 4 (delivery D builds it).
	return contracts.ExternalGroup{}, contracts.ErrGroupNotManageable
}

// SetMembers is delivery D's.
func (p *provisioner) SetMembers(context.Context, contracts.Actor, string, []string) (contracts.MembersResult, error) {
	// Task 4 (delivery D builds it).
	return contracts.MembersResult{}, contracts.ErrGroupNotManageable
}

// DeleteGroup is delivery D's.
func (p *provisioner) DeleteGroup(context.Context, contracts.Actor, string) error {
	// Task 4 (delivery D builds it).
	return contracts.ErrGroupNotManageable
}
