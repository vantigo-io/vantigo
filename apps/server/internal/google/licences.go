package google

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the licences (Google Workspace design G17, G20): a licence
// is productId/skuId, assigned through the Enterprise License Manager's
// licenseAssignments as the admin to impersonate with apps.licensing. The
// module assigns the licences of the person's groups the user does not hold
// yet, and removes only those it assigned itself (google.accounts.
// licences_assigned) once no group names them; an administrator's own
// assignment is the organisation's. The organisation's licences are read by
// listForProduct and cached on the connection, the choices of the licences
// trait. A licence's path carries the user's primary email, which the log
// never names (shownAs).

// knownProducts are the product ids whose SKUs GET /connection/skus reads:
// Google Workspace, Cloud Identity Free and Premium, Google Voice, and the
// archived user licence. A product the organisation holds nothing of, or
// that Google refuses, counts as none.
var knownProducts = []string{"Google-Apps", "101001", "101005", "101033", "101034"}

// The licences' bounds.
const (
	// maxCachedSkus is how many of the organisation's licences the
	// connection's row keeps.
	maxCachedSkus = 200
	// maxAssignmentPages bounds the pages of one product's assignments read.
	maxAssignmentPages = 10
)

// licencePart is a product id or a SKU id as a licence names it.
var licencePart = regexp.MustCompile(`^[A-Za-z0-9-]{1,40}$`)

// licence is a licence's two ids.
type licence struct {
	product, sku string
}

// parseLicence reads productId/skuId; false when it is not one.
func parseLicence(s string) (licence, bool) {
	product, sku, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || !licencePart.MatchString(product) || !licencePart.MatchString(sku) {
		return licence{}, false
	}
	return licence{product: product, sku: sku}, true
}

func (l licence) String() string { return l.product + "/" + l.sku }

// code is licence_unavailable with the SKU id after it, as Staff's codes
// carry it (a code's suffix admits no slash).
func (l licence) code() string {
	if codeSuffix.MatchString(l.sku) {
		return contracts.ReportedLicenceUnavailable + ":" + l.sku
	}
	return contracts.ReportedLicenceUnavailable
}

// url is the licence's assignment of the user email, and the path the log
// names in its place.
func (l licence) url(email string) (string, callOption) {
	path := "/product/" + url.PathEscape(l.product) + "/sku/" + url.PathEscape(l.sku) + "/user/"
	return licensingBase + path + url.PathEscape(email), shownAs("/apps/licensing/v1" + path + "{user}")
}

// held reports whether the user email holds l: licenseAssignments.get, 404
// for not.
func (p *provisioner) held(ctx context.Context, s session, l licence, email string) (bool, error) {
	target, shown := l.url(email)
	err := p.keys.c.get(ctx, s.licences, target, nil, shown)
	switch {
	case err == nil:
		return true, nil
	case isStatus(err, http.StatusNotFound):
		return false, nil
	}
	return false, err
}

// reconcileLicences brings the user's licences to those wanted names: the
// missing assigned (get, then insert — any refusal of the insert, none left
// among them, reported licence_unavailable:<skuId>), then every licence the
// module itself assigned that wanted no longer names removed. A licence an
// administrator assigned is left. It answers the codes reported and the
// event's codes, and records the module's licences. With nothing wanted and
// nothing of the module's held it asks Google nothing.
func (p *provisioner) reconcileLicences(ctx context.Context, s session, u directoryUser, row store.GoogleAccount, wantedValues []string) ([]string, []string, error) {
	var wanted []licence
	var reported []string
	for _, v := range wantedValues {
		l, ok := parseLicence(v)
		switch {
		case !ok:
			reported = append(reported, contracts.ReportedLicenceUnavailable)
		case !slices.Contains(wanted, l):
			wanted = append(wanted, l)
		}
	}
	mine := slices.Clone(row.LicencesAssigned)
	if len(wanted) == 0 && len(mine) == 0 {
		return reported, nil, nil
	}
	var codes []string
	assigned, removed := 0, 0
	for _, l := range wanted {
		if slices.Contains(mine, l.String()) {
			continue
		}
		has, err := p.held(ctx, s, l, u.PrimaryEmail)
		if err != nil {
			return nil, nil, errors.Join(err, p.recordLicences(ctx, s, u.ID, row, mine))
		}
		if has {
			continue // an administrator's assignment: not the module's
		}
		insert := licensingBase + "/product/" + url.PathEscape(l.product) + "/sku/" + url.PathEscape(l.sku) + "/user"
		err = p.keys.c.send(ctx, s.licences, http.MethodPost, insert, map[string]any{"userId": u.PrimaryEmail}, nil, noRetry())
		switch {
		case err == nil:
			assigned++
			mine = append(mine, l.String())
		case isStatus(err, http.StatusConflict):
			// Assigned already, by a call whose answer was lost: the
			// module's.
			mine = append(mine, l.String())
		case isRefusal(err):
			reported = append(reported, l.code())
		default:
			return nil, nil, errors.Join(err, p.recordLicences(ctx, s, u.ID, row, mine))
		}
	}
	for _, v := range slices.Clone(mine) {
		l, ok := parseLicence(v)
		if !ok || slices.Contains(wanted, l) {
			continue
		}
		target, shown := l.url(u.PrimaryEmail)
		err := p.keys.c.send(ctx, s.licences, http.MethodDelete, target, nil, nil, shown)
		switch {
		case err == nil, isStatus(err, http.StatusNotFound):
			removed++
			mine = slices.DeleteFunc(mine, func(m string) bool { return m == v })
		case isRefusal(err):
			codes = append(codes, "licences_not_removed")
		default:
			return nil, nil, errors.Join(err, p.recordLicences(ctx, s, u.ID, row, mine))
		}
	}
	if assigned > 0 {
		codes = append(codes, fmt.Sprintf("licences_assigned:%d", assigned))
	}
	if removed > 0 {
		codes = append(codes, fmt.Sprintf("licences_removed:%d", removed))
	}
	return reported, codes, p.recordLicences(ctx, s, u.ID, row, mine)
}

// recordLicences records mine as the licences the module assigned the user
// when they differ from row's.
func (p *provisioner) recordLicences(ctx context.Context, s session, userID string, row store.GoogleAccount, mine []string) error {
	mine = orderedLicences(mine)
	if slices.Equal(mine, orderedLicences(row.LicencesAssigned)) {
		return nil
	}
	if err := s.q.SetAccountLicences(ctx, store.SetAccountLicencesParams{LicencesAssigned: mine, Now: p.now(), UserID: userID}); err != nil {
		return fmt.Errorf("google: record the module's licences: %w", err)
	}
	return nil
}

// orderedLicences is values sorted, each once, never nil.
func orderedLicences(values []string) []string {
	out := slices.Sorted(slices.Values(values))
	out = slices.Compact(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// removeAllLicences is the leaver's step (G19): every licence the module
// assigned, and every licence of the organisation's cached ones the user
// holds, removed — Google bills a suspended user's licence (U16) — and the
// module's licences forgotten.
func (p *provisioner) removeAllLicences(ctx context.Context, s session, u directoryUser, row store.GoogleAccount) (string, error) {
	var candidates []licence
	for _, v := range row.LicencesAssigned {
		if l, ok := parseLicence(v); ok && !slices.Contains(candidates, l) {
			candidates = append(candidates, l)
		}
	}
	skus, err := cachedSkus(s.conn)
	if err != nil {
		return "", err
	}
	for _, c := range skus {
		if l, ok := parseLicence(c.ProductID + "/" + c.SkuID); ok && !slices.Contains(candidates, l) {
			candidates = append(candidates, l)
		}
	}
	removed := 0
	for _, l := range candidates {
		target, shown := l.url(u.PrimaryEmail)
		err := p.keys.c.send(ctx, s.licences, http.MethodDelete, target, nil, nil, shown)
		switch {
		case err == nil:
			removed++
		case isStatus(err, http.StatusNotFound), isStatus(err, http.StatusBadRequest) && !slices.Contains(row.LicencesAssigned, l.String()):
			// Not held: Google answers 404 (or 400 for a SKU of a product
			// the user has none of).
		default:
			return "", err
		}
	}
	if err := s.q.SetAccountLicences(ctx, store.SetAccountLicencesParams{LicencesAssigned: []string{}, Now: p.now(), UserID: u.ID}); err != nil {
		return "", fmt.Errorf("google: forget the module's licences: %w", err)
	}
	if removed == 0 {
		return "", nil
	}
	return fmt.Sprintf("licences_removed:%d", removed), nil
}

// cachedSku is one licence as connection.skus keeps it.
type cachedSku struct {
	ProductID string `json:"productId"`
	SkuID     string `json:"skuId"`
	Name      string `json:"name"`
	Assigned  int32  `json:"assigned"`
}

// cachedSkus reads the licences connection.skus keeps; an empty column is
// none.
func cachedSkus(conn store.GoogleConnection) ([]cachedSku, error) {
	var skus []cachedSku
	if len(conn.Skus) == 0 {
		return skus, nil
	}
	if err := json.Unmarshal(conn.Skus, &skus); err != nil {
		return nil, fmt.Errorf("google: read the cached licences: %w", err)
	}
	return skus, nil
}

// skuBody is a cached licence as the contract answers it.
func skuBody(c cachedSku) gen.GoogleSku {
	return gen.GoogleSku{ProductId: c.ProductID, SkuId: c.SkuID, Value: c.ProductID + "/" + c.SkuID, Name: c.Name, Assigned: c.Assigned}
}

// readSkus reads the licences the organisation holds: for each known
// product, listForProduct's assignments counted by SKU, a product refused
// (4xx) counting as none.
func (p *provisioner) readSkus(ctx context.Context, s session) ([]cachedSku, error) {
	customer := deref(s.conn.CustomerID)
	if customer == "" {
		customer = deref(s.conn.PrimaryDomain)
	}
	if customer == "" {
		return nil, errNotConfigured
	}
	bySku := map[string]*cachedSku{}
	for _, product := range knownProducts {
		token := ""
		for range maxAssignmentPages {
			v := url.Values{"customerId": {customer}, "maxResults": {"1000"}}
			if token != "" {
				v.Set("pageToken", token)
			}
			var page struct {
				Items []struct {
					ProductID string `json:"productId"`
					SkuID     string `json:"skuId"`
					SkuName   string `json:"skuName"`
				} `json:"items"`
				NextPageToken string `json:"nextPageToken"`
			}
			err := p.keys.c.get(ctx, s.licences, licensingBase+"/product/"+url.PathEscape(product)+"/users?"+v.Encode(), &page)
			if isRefusal(err) && !isStatus(err, http.StatusTooManyRequests) {
				break
			}
			if err != nil {
				return nil, err
			}
			for _, it := range page.Items {
				if !licencePart.MatchString(it.ProductID) || !licencePart.MatchString(it.SkuID) {
					continue
				}
				key := it.ProductID + "/" + it.SkuID
				c := bySku[key]
				if c == nil {
					name := strings.TrimSpace(it.SkuName)
					if name == "" {
						name = it.SkuID
					}
					c = &cachedSku{ProductID: it.ProductID, SkuID: it.SkuID, Name: cut(name, 100)}
					bySku[key] = c
				}
				c.Assigned++
			}
			if token = page.NextPageToken; token == "" {
				break
			}
		}
	}
	out := make([]cachedSku, 0, len(bySku))
	for _, c := range bySku {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b cachedSku) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ProductID, b.ProductID), strings.Compare(a.SkuID, b.SkuID))
	})
	if len(out) > maxCachedSkus {
		out = out[:maxCachedSkus]
	}
	return out, nil
}

// GetGoogleConnectionSkus reads the organisation's licences from Google,
// caches them on the connection and answers them; 503 credential_invalid
// when no token could be had for the licences' scope, google_unreachable
// when Google did not answer. (GET /api/v1/google/connection/skus)
func (s *server) GetGoogleConnectionSkus(ctx context.Context, _ gen.GetGoogleConnectionSkusRequestObject) (gen.GetGoogleConnectionSkusResponseObject, error) {
	p := &provisioner{deps: s.deps, keys: s.keys}
	sess, err := p.open(ctx, false)
	var skus []cachedSku
	if err == nil {
		skus, err = p.readSkus(ctx, sess)
	}
	switch {
	case err != nil && ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(unavailable(err), contracts.ErrProviderUnavailable):
		return gen.GetGoogleConnectionSkus503ApplicationProblemPlusJSONResponse(problem(http.StatusServiceUnavailable, reasonCredentialInvalid,
			"Google Workspace unavailable", "No key fetched a token for the licences: test the connection, and check that the licences' scope is delegated.")), nil
	case err != nil:
		s.keys.c.logger.InfoContext(ctx, "google: the licences could not be read", "error", err.Error())
		return gen.GetGoogleConnectionSkus503ApplicationProblemPlusJSONResponse(problem(http.StatusServiceUnavailable, reasonGoogleUnreachable,
			"Google did not answer", "Google did not answer. Try again.")), nil
	}
	data, err := json.Marshal(skus)
	if err != nil {
		return nil, fmt.Errorf("google: encode the licences: %w", err)
	}
	if err := s.q.SetSkus(ctx, data); err != nil {
		return nil, fmt.Errorf("google: cache the licences: %w", err)
	}
	out := make(gen.GetGoogleConnectionSkus200JSONResponse, 0, len(skus))
	for _, c := range skus {
		out = append(out, skuBody(c))
	}
	return out, nil
}
