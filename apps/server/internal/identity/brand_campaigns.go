package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/brandscale"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/logoimage"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// Brand campaigns (brands design B22-B24): a dated overlay on one brand. A
// campaign has no draft: it is live on save while enabled. The pure rules
// (active, overlap, the next change) are brand_campaign_active.go; here are the
// inputs, the writes under the brand's row lock, and the resolution.

// Audit actions (B34).
const (
	auditCampaignCreated     = "brand.campaign_created"
	auditCampaignUpdated     = "brand.campaign_updated"
	auditCampaignDeleted     = "brand.campaign_deleted"
	auditCampaignLogoSet     = "brand.campaign_logo_set"
	auditCampaignLogoRemoved = "brand.campaign_logo_removed"

	codeCampaignOverlap = "campaign_overlap"
	codeCampaignTooLong = "campaign_too_long"

	campaignNameMax = 100
)

// campaignOverlay is the stored overlay: inputs only (B37, B38). Its tokens are
// generated at resolution, from the brand's radius, so a change of the brand's
// radius or of the scale algorithm reaches a campaign without a rewrite.
type campaignOverlay struct {
	Primary string               `json:"primary,omitempty"`
	Title   string               `json:"title,omitempty"`
	Logos   map[string]brandLogo `json:"logos,omitempty"`
}

// campaignRow is a stored campaign with its overlay parsed.
type campaignRow struct {
	store.IdentityBrandCampaign
	overlay campaignOverlay
}

func loadCampaignRow(c store.IdentityBrandCampaign) (campaignRow, error) {
	row := campaignRow{IdentityBrandCampaign: c}
	if len(c.Overlay) > 0 {
		if err := json.Unmarshal(c.Overlay, &row.overlay); err != nil {
			return campaignRow{}, fmt.Errorf("identity: read a campaign overlay: %w", err)
		}
	}
	if row.overlay.Logos == nil {
		row.overlay.Logos = map[string]brandLogo{}
	}
	return row, nil
}

func loadCampaignRows(rows []store.IdentityBrandCampaign) ([]campaignRow, error) {
	out := make([]campaignRow, 0, len(rows))
	for _, r := range rows {
		c, err := loadCampaignRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (c campaignRow) span() campaignSpan {
	return campaignSpan{StartsOn: c.StartsOn.Time, EndsOn: c.EndsOn.Time, Yearly: c.Yearly}
}

func marshalOverlay(o campaignOverlay) []byte {
	b, err := json.Marshal(o)
	if err != nil {
		panic("identity: marshal a campaign overlay: " + err.Error()) // impossible: plain types
	}
	return b
}

// campaignInput is what a person may send (B37): inputs only.
type campaignInput struct {
	Name         string
	StartsOn     time.Time
	EndsOn       time.Time
	Yearly       bool
	Enabled      bool
	AffectsEmail bool
	Primary      string
	Title        string
}

func (in campaignInput) span() campaignSpan {
	return campaignSpan{StartsOn: in.StartsOn, EndsOn: in.EndsOn, Yearly: in.Yearly}
}

func invalidCampaignField(field, message string) *brandRefusal {
	return refuseBrand(http.StatusBadRequest, codeBrandNotAccepted, message, map[string][]string{field: {message}})
}

// campaignInputOf reads and validates a create or save request: a member the
// contract does not list is refused, the dates must make a range, a yearly
// range is at most a year, and the overlay's colour is checked as a brand's is
// (B4, B38).
func campaignInputOf(body *gen.BrandCampaignInput) (campaignInput, *brandRefusal) {
	if body == nil {
		return campaignInput{}, refuseBrand(http.StatusBadRequest, codeBrandNotAccepted, "The request needs a body.", nil)
	}
	if refusal := extraMembers(body.AdditionalProperties); refusal != nil {
		return campaignInput{}, refusal
	}
	in := campaignInput{Enabled: true}
	if body.Overlay != nil {
		if refusal := extraMembers(body.Overlay.AdditionalProperties); refusal != nil {
			return campaignInput{}, refusal
		}
		in.Primary = strings.ToLower(strings.TrimSpace(deref(body.Overlay.Primary)))
		in.Title = strings.TrimSpace(deref(body.Overlay.Title))
	}
	in.Name = strings.TrimSpace(body.Name)
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > campaignNameMax {
		return campaignInput{}, invalidCampaignField("name", "The name is 1 to 100 characters.")
	}
	if body.Yearly != nil {
		in.Yearly = *body.Yearly
	}
	if body.Enabled != nil {
		in.Enabled = *body.Enabled
	}
	if body.AffectsEmail != nil {
		in.AffectsEmail = *body.AffectsEmail
	}
	in.StartsOn, in.EndsOn = body.StartsOn.Time, body.EndsOn.Time
	// Dates are calendar days: drop whatever clock the decoder left on them.
	in.StartsOn = time.Date(in.StartsOn.Year(), in.StartsOn.Month(), in.StartsOn.Day(), 0, 0, 0, 0, time.UTC)
	in.EndsOn = time.Date(in.EndsOn.Year(), in.EndsOn.Month(), in.EndsOn.Day(), 0, 0, 0, 0, time.UTC)
	for field, d := range map[string]time.Time{"startsOn": in.StartsOn, "endsOn": in.EndsOn} {
		if d.Year() < campaignMinYear || d.Year() > campaignMaxYear {
			return campaignInput{}, invalidCampaignField(field, fmt.Sprintf("The date is between the years %d and %d.", campaignMinYear, campaignMaxYear))
		}
	}
	if in.EndsOn.Before(in.StartsOn) {
		return campaignInput{}, invalidCampaignField("endsOn", "The campaign cannot end before it starts.")
	}
	if in.Yearly && campaignDays(in.span()) > campaignMaxYearlyDays {
		return campaignInput{}, refuseBrand(http.StatusUnprocessableEntity, codeCampaignTooLong,
			"A campaign that repeats every year can last at most 365 days.", map[string][]string{"endsOn": {"At most 365 days."}})
	}
	if utf8.RuneCountInString(in.Title) > brandTitleMax {
		return campaignInput{}, invalidCampaignField("title", "The title is at most 60 characters.")
	}
	if in.Primary != "" {
		if !hexPrimary.MatchString(in.Primary) {
			return campaignInput{}, invalidCampaignField("primary", "The primary colour is written #rrggbb.")
		}
		if refusal := checkOverlayPrimary(in.Primary); refusal != nil {
			return campaignInput{}, refusal
		}
	}
	return in, nil
}

// checkOverlayPrimary is B3 and B4 for an overlay's primary: it builds a scale
// and passes every colour pair, as a brand's must to be published.
func checkOverlayPrimary(primary string) *brandRefusal {
	if _, _, err := computeTokens(primary, defaultBrandRadius); errors.Is(err, brandscale.ErrPrimaryRange) {
		return refuseBrand(http.StatusUnprocessableEntity, codeBrandPrimaryRange,
			"The primary colour is too light or too dark to build a scale from; choose one that is not near white or black.",
			map[string][]string{"primary": {"Choose a colour that is not near white or black."}})
	} else if err != nil {
		return invalidCampaignField("primary", "The primary colour is not valid.")
	}
	if failing := failingPairs(contrastChecks(brandVersion{Primary: primary})); len(failing) > 0 {
		return refuseBrand(http.StatusUnprocessableEntity, codeBrandContrast,
			"Some colours are too close to their background to read: "+strings.Join(failing, ", ")+".",
			map[string][]string{"pairs": failing, "primary": {"This colour does not read well enough."}})
	}
	return nil
}

// foldOverlay is v with a campaign's overlay laid over it (B22, B24): its
// primary rebuilt into tokens on the brand's own radius, its title, and its
// logos slot by slot. The name, the radius and the support contacts stay the
// brand's.
func foldOverlay(v brandVersion, o campaignOverlay) (brandVersion, error) {
	if o.Primary != "" {
		tokens, _, err := computeTokens(o.Primary, v.Radius)
		if err != nil {
			return v, fmt.Errorf("identity: build a campaign's colours: %w", err)
		}
		v.Primary, v.Tokens, v.Algorithm = o.Primary, tokens, brandscale.Algorithm
	}
	if o.Title != "" {
		v.Title = o.Title
	}
	if len(o.Logos) > 0 {
		logos := make(map[string]brandLogo, len(v.Logos)+len(o.Logos))
		for k, l := range v.Logos {
			logos[k] = l
		}
		for k, l := range o.Logos {
			logos[k] = l
		}
		v.Logos = logos
	}
	return v, nil
}

// --- resolution -------------------------------------------------------------

// campaignState is what the campaigns of the resolved brand make of it today.
type campaignState struct {
	active     *campaignRow
	validUntil *time.Time
}

// campaignsOf is the state of brandID's campaigns at now (B22-B24): the one in
// force on the business calendar day, and the next time that changes.
func (s *server) campaignsOf(ctx context.Context, brandID uuid.UUID, now time.Time) (campaignState, error) {
	raw, err := s.q.ListEnabledBrandCampaigns(ctx, brandID)
	if err != nil {
		return campaignState{}, fmt.Errorf("identity: read a brand's campaigns: %w", err)
	}
	if len(raw) == 0 {
		return campaignState{}, nil
	}
	rows, err := loadCampaignRows(raw)
	if err != nil {
		return campaignState{}, err
	}
	loc, today, err := s.businessDay(ctx, now)
	if err != nil {
		return campaignState{}, err
	}
	spans := make([]campaignSpan, len(rows))
	for i, r := range rows {
		spans[i] = r.span()
	}
	var st campaignState
	if i := activeCampaign(spans, today); i >= 0 {
		st.active = &rows[i]
	}
	if next, ok := nextCampaignChange(spans, today); ok {
		at := time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, loc)
		st.validUntil = &at
	}
	return st, nil
}

// businessDay is the business time zone and the calendar day now falls on in
// it, at midnight UTC.
func (s *server) businessDay(ctx context.Context, now time.Time) (*time.Location, time.Time, error) {
	if s.deps.Calendar == nil {
		y, m, d := now.UTC().Date()
		return time.UTC, time.Date(y, m, d, 0, 0, 0, 0, time.UTC), nil
	}
	loc, err := s.deps.Calendar.Zone(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	day, err := s.deps.Calendar.Day(ctx, now)
	if err != nil {
		return nil, time.Time{}, err
	}
	return loc, day, nil
}

// resolveLook is resolveUser with the brand's active campaign folded into the
// version a person sees (B13, B22): what the stylesheet and the payload serve.
func (s *server) resolveLook(ctx context.Context, userID uuid.UUID) (resolution, error) {
	res, err := s.resolveUser(ctx, userID)
	if err != nil {
		return resolution{}, err
	}
	return s.applyCampaign(ctx, res)
}

// applyCampaign folds the active campaign of res's brand into its version, and
// sets validUntil. It is for the looks the workspace shows; contracts.Brands,
// which documents use, never goes through it (B24).
func (s *server) applyCampaign(ctx context.Context, res resolution) (resolution, error) {
	st, err := s.campaignsOf(ctx, res.brand.ID, s.deps.Clock())
	if err != nil {
		return resolution{}, err
	}
	res.validUntil = st.validUntil
	if st.active != nil {
		v, err := foldOverlay(res.version, st.active.overlay)
		if err != nil { // a colour the scale can no longer build: the brand's own look, not a failed page
			s.deps.Logger.ErrorContext(ctx, "identity: a campaign's overlay could not be applied", "campaign_id", st.active.ID, "error", err.Error())
		} else {
			res.version, res.campaign = v, st.active
		}
	}
	return res, nil
}

// withCampaign marks a resolved payload with the campaign in force and the next
// time it changes (B13, B24), and moves its cache key so a client sees the
// change.
func withCampaign(payload gen.ResolvedBrand, res resolution) gen.ResolvedBrand {
	payload.ValidUntil = res.validUntil
	if res.campaign != nil {
		payload.Campaign = &gen.BrandCampaignRef{Id: res.campaign.ID, Name: res.campaign.Name}
		payload.Version = fmt.Sprintf("%s-c%s-%d", payload.Version, res.campaign.ID, res.campaign.Revision)
	}
	return payload
}

// campaignLogoInForce reports whether an enabled campaign of a published brand
// names the asset and is active today (B15): the anonymous asset route serves a
// campaign's logo only while it is in force.
func (s *server) campaignLogoInForce(ctx context.Context, sha string) (bool, error) {
	raw, err := s.q.ListCampaignsNamingBrandLogo(ctx, sha)
	if err != nil {
		return false, fmt.Errorf("identity: read the campaigns naming a logo: %w", err)
	}
	if len(raw) == 0 {
		return false, nil
	}
	_, today, err := s.businessDay(ctx, s.deps.Clock())
	if err != nil {
		return false, err
	}
	for _, r := range raw {
		if campaignActive(campaignRow{IdentityBrandCampaign: r}.span(), today) {
			return true, nil
		}
	}
	return false, nil
}

// --- the wire ---------------------------------------------------------------

func genLogosOf(logos map[string]brandLogo) gen.BrandLogos {
	out := gen.BrandLogos{}
	for slot, l := range logos {
		ref := &gen.BrandLogo{Sha256: l.SHA256, ContentType: gen.BrandLogoContentType(l.ContentType)}
		switch slot {
		case "mark_light":
			out.MarkLight = ref
		case "mark_dark":
			out.MarkDark = ref
		case "icon":
			out.Icon = ref
		case "wordmark":
			out.Wordmark = ref
		}
	}
	return out
}

func campaignStatus(c campaignRow, today time.Time) gen.BrandCampaignStatus {
	switch {
	case !c.Enabled:
		return gen.BrandCampaignStatusDisabled
	case campaignActive(c.span(), today):
		return gen.BrandCampaignStatusActive
	case !c.Yearly && today.After(c.EndsOn.Time):
		return gen.BrandCampaignStatusEnded
	default:
		return gen.BrandCampaignStatusUpcoming
	}
}

func genCampaign(c campaignRow, today time.Time) gen.BrandCampaign {
	return gen.BrandCampaign{
		Id: c.ID, BrandId: c.BrandID, Name: c.Name,
		StartsOn: openapi_types.Date{Time: c.StartsOn.Time}, EndsOn: openapi_types.Date{Time: c.EndsOn.Time},
		Yearly: c.Yearly, Enabled: c.Enabled, AffectsEmail: c.AffectsEmail, Revision: c.Revision,
		Overlay: gen.BrandCampaignOverlay{Primary: opt(c.overlay.Primary), Title: opt(c.overlay.Title), Logos: genLogosOf(c.overlay.Logos)},
		Status:  campaignStatus(c, today),
	}
}

// auditCampaignState is what an audit row carries of a campaign: its inputs and
// the names of its logo slots, never a logo's bytes (B34).
func auditCampaignState(c campaignRow) map[string]any {
	return map[string]any{
		"id": c.ID, "brandId": c.BrandID, "name": c.Name,
		"startsOn": c.StartsOn.Time.Format(time.DateOnly), "endsOn": c.EndsOn.Time.Format(time.DateOnly),
		"yearly": c.Yearly, "enabled": c.Enabled, "affectsEmail": c.AffectsEmail,
		"primary": c.overlay.Primary, "title": c.overlay.Title, "logos": slotNames(c.overlay.Logos),
	}
}

// --- writes -----------------------------------------------------------------

// campaignWrite is the change a write makes to a brand's campaigns, under the
// brand's row lock: it answers the campaign it wrote (nil for a delete), the
// state before, or a refusal.
type campaignWrite func(ctx context.Context, q *store.Queries, b brandRow, now time.Time) (before any, after *campaignRow, refusal *brandRefusal, err error)

// campaignOutcome is the result of changeCampaign.
type campaignOutcome struct {
	campaign *campaignRow
	refusal  *brandRefusal
	notFound bool
}

var errCampaignNotFound = errors.New("campaign not found")

// changeCampaign is every campaign write: under the brand's row lock (which also
// serialises the overlap check, B23) write runs, the open sessions are told
// (B18) and the change is audited (B34), in one transaction.
func (s *server) changeCampaign(ctx context.Context, brandID uuid.UUID, action string, write campaignWrite) (campaignOutcome, error) {
	c, err := s.brandCall(ctx)
	if err != nil {
		return campaignOutcome{}, err
	}
	var out campaignOutcome
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		raw, err := q.LockBrand(ctx, brandID)
		if errors.Is(err, pgx.ErrNoRows) {
			out.notFound = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: lock a brand: %w", err)
		}
		b, err := loadBrandRow(raw)
		if err != nil {
			return err
		}
		if b.State == "archived" {
			out.refusal = errArchived
			return nil
		}
		before, after, refusal, err := write(ctx, q, b, c.now)
		if errors.Is(err, errCampaignNotFound) {
			out.notFound = true
			return nil
		}
		if err != nil {
			return err
		}
		if refusal != nil {
			out.refusal = refusal
			return nil
		}
		out.campaign = after
		var afterState any = struct{}{}
		if after != nil {
			afterState = auditCampaignState(*after)
		}
		if err := notifyBrands(ctx, tx, eventBrandChanged); err != nil {
			return err
		}
		return writeAudit(ctx, q, c.r, c.now, auditEvent{
			actor: &c.p.UserID, action: action, before: before, after: afterState, mfa: c.p.MFAVerified,
		})
	})
	return out, err
}

// checkCampaignOverlap refuses a campaign that would be active on a date one of
// the brand's other enabled campaigns is (B23).
func checkCampaignOverlap(ctx context.Context, q *store.Queries, brandID, self uuid.UUID, span campaignSpan) (*brandRefusal, error) {
	raw, err := q.ListEnabledBrandCampaigns(ctx, brandID)
	if err != nil {
		return nil, fmt.Errorf("identity: read a brand's campaigns: %w", err)
	}
	others, err := loadCampaignRows(raw)
	if err != nil {
		return nil, err
	}
	for _, o := range others {
		if o.ID == self {
			continue
		}
		if day, clash := campaignsOverlap(span, o.span()); clash {
			msg := fmt.Sprintf("The campaign %q is active on %s as well; two campaigns of a brand cannot be active on the same day.", o.Name, day.Format(time.DateOnly))
			return refuseBrand(http.StatusConflict, codeCampaignOverlap, msg, map[string][]string{"startsOn": {msg}}), nil
		}
	}
	return nil, nil
}

// campaignOverlayOf is the overlay a save carries over: the inputs it names and
// the logos the campaign already holds.
func campaignOverlayOf(in campaignInput, logos map[string]brandLogo) campaignOverlay {
	return campaignOverlay{Primary: in.Primary, Title: in.Title, Logos: logos}
}

// GetIdentityBrandsByIdCampaigns List a brand's campaigns
// (GET /api/v1/identity/brands/{id}/campaigns)
func (s *server) GetIdentityBrandsByIdCampaigns(ctx context.Context, req gen.GetIdentityBrandsByIdCampaignsRequestObject) (gen.GetIdentityBrandsByIdCampaignsResponseObject, error) {
	if _, found, err := s.loadBrand(ctx, s.q, uuid.UUID(req.Id)); err != nil {
		return nil, err
	} else if !found {
		return gen.GetIdentityBrandsByIdCampaigns404Response{}, nil
	}
	raw, err := s.q.ListBrandCampaigns(ctx, uuid.UUID(req.Id))
	if err != nil {
		return nil, fmt.Errorf("identity: list a brand's campaigns: %w", err)
	}
	rows, err := loadCampaignRows(raw)
	if err != nil {
		return nil, err
	}
	_, today, err := s.businessDay(ctx, s.deps.Clock())
	if err != nil {
		return nil, err
	}
	items := make([]gen.BrandCampaign, 0, len(rows))
	for _, r := range rows {
		items = append(items, genCampaign(r, today))
	}
	return gen.GetIdentityBrandsByIdCampaigns200JSONResponse{Items: items, Today: openapi_types.Date{Time: today}}, nil
}

// PostIdentityBrandsByIdCampaigns Create a campaign
// (POST /api/v1/identity/brands/{id}/campaigns)
func (s *server) PostIdentityBrandsByIdCampaigns(ctx context.Context, req gen.PostIdentityBrandsByIdCampaignsRequestObject) (gen.PostIdentityBrandsByIdCampaignsResponseObject, error) {
	reply := func(r *brandRefusal) (gen.PostIdentityBrandsByIdCampaignsResponseObject, error) {
		switch r.status {
		case http.StatusBadRequest:
			return gen.PostIdentityBrandsByIdCampaigns400JSONResponse(r.body()), nil
		case http.StatusConflict:
			return gen.PostIdentityBrandsByIdCampaigns409JSONResponse(r.body()), nil
		default:
			return gen.PostIdentityBrandsByIdCampaigns422JSONResponse(r.body()), nil
		}
	}
	in, refusal := campaignInputOf(req.Body)
	if refusal != nil {
		return reply(refusal)
	}
	brandID := uuid.UUID(req.Id)
	out, err := s.changeCampaign(ctx, brandID, auditCampaignCreated, func(ctx context.Context, q *store.Queries, _ brandRow, now time.Time) (any, *campaignRow, *brandRefusal, error) {
		if in.Enabled {
			if refusal, err := checkCampaignOverlap(ctx, q, brandID, uuid.Nil, in.span()); refusal != nil || err != nil {
				return nil, nil, refusal, err
			}
		}
		p, _ := callerFrom(ctx)
		raw, err := q.InsertBrandCampaign(ctx, store.InsertBrandCampaignParams{
			ID: uuid.New(), BrandID: brandID, Name: in.Name, StartsOn: pgDate(in.StartsOn), EndsOn: pgDate(in.EndsOn),
			Yearly: in.Yearly, Overlay: marshalOverlay(campaignOverlayOf(in, nil)), AffectsEmail: in.AffectsEmail,
			Enabled: in.Enabled, Now: now, UserID: &p.UserID,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("identity: create a campaign: %w", err)
		}
		row, err := loadCampaignRow(raw)
		return struct{}{}, &row, nil, err
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PostIdentityBrandsByIdCampaigns404Response{}, nil
	case out.refusal != nil:
		return reply(out.refusal)
	}
	return gen.PostIdentityBrandsByIdCampaigns201JSONResponse(s.campaignWire(ctx, *out.campaign)), nil
}

// campaignWire is a campaign as the admin reads it, its status at the business
// calendar day of now.
func (s *server) campaignWire(ctx context.Context, c campaignRow) gen.BrandCampaign {
	_, today, err := s.businessDay(ctx, s.deps.Clock())
	if err != nil {
		today = s.deps.Clock().UTC().Truncate(24 * time.Hour)
	}
	return genCampaign(c, today)
}

// PutIdentityBrandsByIdCampaignsByCampaignId Save a campaign
// (PUT /api/v1/identity/brands/{id}/campaigns/{campaignId})
func (s *server) PutIdentityBrandsByIdCampaignsByCampaignId(ctx context.Context, req gen.PutIdentityBrandsByIdCampaignsByCampaignIdRequestObject) (gen.PutIdentityBrandsByIdCampaignsByCampaignIdResponseObject, error) {
	reply := func(r *brandRefusal) (gen.PutIdentityBrandsByIdCampaignsByCampaignIdResponseObject, error) {
		switch r.status {
		case http.StatusBadRequest:
			return gen.PutIdentityBrandsByIdCampaignsByCampaignId400JSONResponse(r.body()), nil
		case http.StatusConflict:
			return gen.PutIdentityBrandsByIdCampaignsByCampaignId409JSONResponse(r.body()), nil
		default:
			return gen.PutIdentityBrandsByIdCampaignsByCampaignId422JSONResponse(r.body()), nil
		}
	}
	in, refusal := campaignInputOf(req.Body)
	if refusal != nil {
		return reply(refusal)
	}
	brandID, id := uuid.UUID(req.Id), uuid.UUID(req.CampaignId)
	out, err := s.changeCampaign(ctx, brandID, auditCampaignUpdated, func(ctx context.Context, q *store.Queries, _ brandRow, now time.Time) (any, *campaignRow, *brandRefusal, error) {
		cur, err := campaignOf(ctx, q, brandID, id)
		if err != nil {
			return nil, nil, nil, err
		}
		if in.Enabled {
			if refusal, err := checkCampaignOverlap(ctx, q, brandID, id, in.span()); refusal != nil || err != nil {
				return nil, nil, refusal, err
			}
		}
		p, _ := callerFrom(ctx)
		raw, err := q.UpdateBrandCampaign(ctx, store.UpdateBrandCampaignParams{
			ID: id, BrandID: brandID, Name: in.Name, StartsOn: pgDate(in.StartsOn), EndsOn: pgDate(in.EndsOn),
			Yearly: in.Yearly, Overlay: marshalOverlay(campaignOverlayOf(in, cur.overlay.Logos)), AffectsEmail: in.AffectsEmail,
			Enabled: in.Enabled, Now: now, UserID: &p.UserID,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("identity: save a campaign: %w", err)
		}
		row, err := loadCampaignRow(raw)
		return auditCampaignState(cur), &row, nil, err
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PutIdentityBrandsByIdCampaignsByCampaignId404Response{}, nil
	case out.refusal != nil:
		return reply(out.refusal)
	}
	return gen.PutIdentityBrandsByIdCampaignsByCampaignId200JSONResponse(s.campaignWire(ctx, *out.campaign)), nil
}

// campaignOf reads a campaign of a brand, errCampaignNotFound when there is none.
func campaignOf(ctx context.Context, q *store.Queries, brandID, id uuid.UUID) (campaignRow, error) {
	raw, err := q.GetBrandCampaign(ctx, store.GetBrandCampaignParams{ID: id, BrandID: brandID})
	if errors.Is(err, pgx.ErrNoRows) {
		return campaignRow{}, errCampaignNotFound
	}
	if err != nil {
		return campaignRow{}, fmt.Errorf("identity: read a campaign: %w", err)
	}
	return loadCampaignRow(raw)
}

// DeleteIdentityBrandsByIdCampaignsByCampaignId Delete a campaign
// (DELETE /api/v1/identity/brands/{id}/campaigns/{campaignId})
func (s *server) DeleteIdentityBrandsByIdCampaignsByCampaignId(ctx context.Context, req gen.DeleteIdentityBrandsByIdCampaignsByCampaignIdRequestObject) (gen.DeleteIdentityBrandsByIdCampaignsByCampaignIdResponseObject, error) {
	brandID, id := uuid.UUID(req.Id), uuid.UUID(req.CampaignId)
	out, err := s.changeCampaign(ctx, brandID, auditCampaignDeleted, func(ctx context.Context, q *store.Queries, _ brandRow, _ time.Time) (any, *campaignRow, *brandRefusal, error) {
		cur, err := campaignOf(ctx, q, brandID, id)
		if err != nil {
			return nil, nil, nil, err
		}
		if _, err := q.DeleteBrandCampaign(ctx, store.DeleteBrandCampaignParams{ID: id, BrandID: brandID}); err != nil {
			return nil, nil, nil, fmt.Errorf("identity: delete a campaign: %w", err)
		}
		return auditCampaignState(cur), nil, nil, nil
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound, out.refusal != nil: // an archived brand's campaigns are not edited
		return gen.DeleteIdentityBrandsByIdCampaignsByCampaignId404Response{}, nil
	}
	return gen.DeleteIdentityBrandsByIdCampaignsByCampaignId204Response{}, nil
}

// --- logos ------------------------------------------------------------------

// setCampaignLogo puts logo (or none) in slot of the campaign's overlay, the
// asset registered in the same transaction. Live at once (B22).
func (s *server) setCampaignLogo(ctx context.Context, brandID, id uuid.UUID, action, slot string, logo *brandLogo) (campaignOutcome, error) {
	return s.changeCampaign(ctx, brandID, action, func(ctx context.Context, q *store.Queries, _ brandRow, now time.Time) (any, *campaignRow, *brandRefusal, error) {
		cur, err := campaignOf(ctx, q, brandID, id)
		if err != nil {
			return nil, nil, nil, err
		}
		overlay := cur.overlay
		logos := make(map[string]brandLogo, len(overlay.Logos)+1)
		for k, l := range overlay.Logos {
			logos[k] = l
		}
		if logo == nil {
			delete(logos, slot)
		} else {
			if err := registerLogo(ctx, q, *logo, now); err != nil {
				return nil, nil, nil, err
			}
			logos[slot] = *logo
		}
		overlay.Logos = logos
		p, _ := callerFrom(ctx)
		raw, err := q.UpdateBrandCampaign(ctx, store.UpdateBrandCampaignParams{
			ID: id, BrandID: brandID, Name: cur.Name, StartsOn: cur.StartsOn, EndsOn: cur.EndsOn, Yearly: cur.Yearly,
			Overlay: marshalOverlay(overlay), AffectsEmail: cur.AffectsEmail, Enabled: cur.Enabled, Now: now, UserID: &p.UserID,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("identity: save a campaign's logo: %w", err)
		}
		row, err := loadCampaignRow(raw)
		return auditCampaignState(cur), &row, nil, err
	})
}

// PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot Set a campaign logo
// (PUT /api/v1/identity/brands/{id}/campaigns/{campaignId}/logos/{slot})
func (s *server) PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot(ctx context.Context, req gen.PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotRequestObject) (gen.PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotResponseObject, error) {
	reply := func(r *brandRefusal) (gen.PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotResponseObject, error) {
		if r.status == http.StatusServiceUnavailable {
			return gen.PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot503JSONResponse(r.body()), nil
		}
		return gen.PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot400JSONResponse(r.body()), nil
	}
	slot, ok := slotOf(string(req.Slot))
	brandID, id := uuid.UUID(req.Id), uuid.UUID(req.CampaignId)
	if !ok {
		return gen.PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot404Response{}, nil
	}
	if _, err := campaignOf(ctx, s.q, brandID, id); errors.Is(err, errCampaignNotFound) {
		return gen.PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot404Response{}, nil
	} else if err != nil {
		return nil, err
	}
	data, refusal := logoimage.Read(req.Body)
	if refusal != nil {
		return reply(logoRefusal(refusal))
	}
	logo, bad, err := s.storeLogo(ctx, slot, data)
	if bad != nil {
		if err != nil {
			s.deps.Logger.ErrorContext(ctx, "identity: a campaign logo could not be written to the store", "error", err.Error())
		}
		return reply(bad)
	}
	if err != nil {
		return nil, err
	}
	out, err := s.setCampaignLogo(ctx, brandID, id, auditCampaignLogoSet, slot, &logo)
	switch {
	case err != nil:
		return nil, err
	case out.notFound, out.refusal != nil: // an archived brand's campaigns are not edited
		return gen.PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot404Response{}, nil
	}
	return gen.PutIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot200JSONResponse(s.campaignWire(ctx, *out.campaign)), nil
}

// DeleteIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot Remove a campaign logo
// (DELETE /api/v1/identity/brands/{id}/campaigns/{campaignId}/logos/{slot})
func (s *server) DeleteIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot(ctx context.Context, req gen.DeleteIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotRequestObject) (gen.DeleteIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotResponseObject, error) {
	slot, ok := slotOf(string(req.Slot))
	if !ok {
		return gen.DeleteIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot404Response{}, nil
	}
	out, err := s.setCampaignLogo(ctx, uuid.UUID(req.Id), uuid.UUID(req.CampaignId), auditCampaignLogoRemoved, slot, nil)
	switch {
	case err != nil:
		return nil, err
	case out.notFound, out.refusal != nil:
		return gen.DeleteIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot404Response{}, nil
	}
	return gen.DeleteIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot200JSONResponse(s.campaignWire(ctx, *out.campaign)), nil
}

type campaignLogoResponse struct{ logoDownload }

func (r campaignLogoResponse) VisitGetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotResponse(w http.ResponseWriter) error {
	r.set(w)
	return r.visit(w)
}

// GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot Get a campaign logo
// (GET /api/v1/identity/brands/{id}/campaigns/{campaignId}/logos/{slot})
func (s *server) GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot(ctx context.Context, req gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotRequestObject) (gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotResponseObject, error) {
	slot, ok := slotOf(string(req.Slot))
	if !ok {
		return gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot404Response{}, nil
	}
	c, err := campaignOf(ctx, s.q, uuid.UUID(req.Id), uuid.UUID(req.CampaignId))
	if errors.Is(err, errCampaignNotFound) {
		return gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	l, has := c.overlay.Logos[slot]
	if !has {
		return gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot404Response{}, nil
	}
	data, contentType, err := s.readLogoObject(ctx, l.SHA256)
	switch {
	case errors.Is(err, errBrandStorageOff):
		return gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot503JSONResponse(storageOffRefusal.body()), nil
	case errors.Is(err, storage.ErrNotExist), errors.Is(err, pgx.ErrNoRows):
		return gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot404Response{}, nil
	case err != nil:
		s.deps.Logger.ErrorContext(ctx, "identity: a campaign logo could not be read from the store", "error", err.Error())
		return gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot503JSONResponse(authErrorBody(codeStorageOff, "The logo could not be read from the store; try again.", nil)), nil
	}
	length := int64(len(data))
	dl := logoDownload{sha: l.SHA256, cacheControl: brandLogoCacheControl}
	if contentType == logoimage.ContentTypeJPEG {
		dl.visit = gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot200ImagejpegResponse{Body: bytes.NewReader(data), ContentLength: length}.VisitGetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotResponse
	} else {
		dl.visit = gen.GetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlot200ImagepngResponse{Body: bytes.NewReader(data), ContentLength: length}.VisitGetIdentityBrandsByIdCampaignsByCampaignIdLogosBySlotResponse
	}
	return campaignLogoResponse{dl}, nil
}
