package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// SetBrandImportNetwork makes the brand logo import resolve with resolver and
// dial with dial, through the same netguard-guarded transport the webhook
// sender uses, so a test reaches a local server by a name that resolves to a
// public address, or proves a private address is refused.
func SetBrandImportNetwork(a *Access, resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error)) {
	a.brandNet = &webhookNetwork{resolver: resolver, dial: dial}
}

// SetBrandMeterProvider builds the brand counters on mp; it must run before
// the module mounts.
func SetBrandMeterProvider(a *Access, mp metric.MeterProvider) { a.brandMP = mp }

// RunBrandImportOnce is one pass of the default brand's logo import, as the
// runner makes it: it reports whether the import is complete.
func RunBrandImportOnce(ctx context.Context, a *Access, d module.Deps) (bool, error) {
	objects, err := newBrandObjects(d)
	if err != nil {
		return false, err
	}
	r := &brandImportRunner{d: d, a: a, objects: objects, metrics: a.brandMetricsFor(), wait: time.Millisecond}
	return r.once(ctx)
}

// EnsureDefaultBrand creates the default brand from the environment's
// branding, as an API mount does; it reports whether this call created it.
func EnsureDefaultBrand(ctx context.Context, d module.Deps) (bool, error) {
	return ensureDefaultBrand(ctx, d, d.Config.Branding)
}

// BrandImportRunners is the runners the module registers for d.
func BrandImportRunners(a *Access, d module.Deps) []module.Runner { return brandImportRunners(a, d) }

// BrandCSS renders the stylesheet of a primary and radius.
func BrandCSS(primary, radius string) (string, error) {
	v, refusal := newBrandVersion(brandInput{Name: "x", Primary: primary, Radius: radius}, nil)
	if refusal != nil {
		return "", errors.New(refusal.message)
	}
	return brandCSS(v), nil
}

// BrandTokensJSON is the JSON of the tokens a primary and radius generate.
func BrandTokensJSON(primary, radius string) ([]byte, error) {
	v, refusal := newBrandVersion(brandInput{Name: "x", Primary: primary, Radius: radius}, nil)
	if refusal != nil {
		return nil, errors.New(refusal.message)
	}
	return json.Marshal(v.Tokens)
}

// BrandChecksPass reports whether a primary passes every contrast check.
func BrandChecksPass(primary string) bool {
	v, refusal := newBrandVersion(brandInput{Name: "x", Primary: primary, Radius: "md"}, nil)
	return refusal == nil && len(failingPairs(contrastChecks(v))) == 0
}

// BrandSupportMessage validates one support contact the way a save does and
// answers whether it is valid.
func BrandSupportValid(email, phone, url string) bool {
	_, bad := validateSupport(brandSupport{Email: email, Phone: phone, URL: url})
	return bad == nil
}
