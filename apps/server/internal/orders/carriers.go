package orders

// Carrier accounts (orders design O51): one carrier's keys under Workspace
// admin › Integrations › Shipping. The carrier is an adapter of
// internal/shipping that SHIPPING_CARRIERS allows; the Mybring user id and
// API key are sealed together by the secrets box under orders' own purpose —
// as Invoices keeps its Vipps keys — and are never answered, logged or sent
// anywhere but to the carrier. Verify makes the carrier's cheapest
// authenticated call and lists the services; the default account per site is
// what a site's shipments and estimates use.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
	"github.com/vantigo-io/vantigo/server/internal/shipping"
	"github.com/vantigo-io/vantigo/server/internal/shipping/bring"
)

const (
	// carrierCredentialPurpose is the secrets box purpose an account's keys
	// are sealed under.
	carrierCredentialPurpose = "orders/carrier-credentials"
	// carrierBring is the one carrier of this phase.
	carrierBring = "bring"

	accountActive   = "active"
	accountArchived = "archived"

	invalidCarrierTitle  = "Invalid carrier account"
	invalidShipmentTitle = "Invalid shipment"
)

// The shipping refusal codes.
const (
	codeCarrierUnavailable     = "carrier_unavailable"
	codeCarrierUnreachable     = "carrier_unreachable"
	codeCarrierRefused         = "carrier_refused"
	codeCarrierNameTaken       = "carrier_account_name_taken"
	codeCarrierAccountArchived = "carrier_account_archived"
	codeNoCarrierAccount       = "no_carrier_account"
	codeServiceUnknown         = "service_unknown"
	codeDeliveryNotPosted      = "delivery_not_posted"
	codeShipmentNotDraft       = "shipment_not_draft"
	codeShipmentPastCancel     = "shipment_past_cancel"
	codeShipmentNotCancellable = "shipment_not_cancellable"
	codeAddressMissing         = "address_missing"
	codeCurrencyMismatch       = "currency_mismatch"
	codeFreightProductInvalid  = "freight_product_invalid"
)

// customerNumberPattern is a carrier's customer number as Bring hands them
// out: letters, digits, hyphens and underscores.
var customerNumberPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,20}$`)

// carrierKeys is the sealed JSON: the Mybring user id and API key.
type carrierKeys struct {
	UID string `json:"uid"`
	Key string `json:"key"`
}

var (
	// errCarrierWithheld is a carrier SHIPPING_CARRIERS does not allow.
	errCarrierWithheld = errors.New("orders: the carrier is not allowed on this installation")
	// errCarrierKeysUnreadable is stored keys the box cannot open: APP_SECRET
	// changed, or the row was altered.
	errCarrierKeysUnreadable = errors.New("orders: the carrier account's keys cannot be opened")
)

// carrierUnavailable is the 503 of a carrier that cannot be called.
func carrierUnavailable(err error) error {
	detail := "The carrier cannot be called."
	switch {
	case errors.Is(err, errCarrierWithheld):
		detail = "The carrier is not allowed on this installation (SHIPPING_CARRIERS)."
	case errors.Is(err, errCarrierKeysUnreadable):
		detail = "The carrier account's keys cannot be read on this server. Enter them again under Integrations."
	case errors.Is(err, shipping.ErrUnauthorized):
		detail = "The carrier refused the account's keys. Check them under Integrations and verify the account."
	}
	return refuse(problem(http.StatusServiceUnavailable, codeCarrierUnavailable, "The carrier is unavailable", detail))
}

// carrierFailed maps a carrier's error to the operation's answer: a refusal
// is 422 carrier_refused with the carrier's own words, refused keys 503
// carrier_unavailable, anything else 503 carrier_unreachable.
func carrierFailed(err error, what string) error {
	var r *shipping.CarrierRefusal
	switch {
	case errors.As(err, &r):
		detail := r.Message
		if detail == "" {
			detail = "The carrier refused the request."
		}
		p := problem(http.StatusUnprocessableEntity, codeCarrierRefused, "The carrier refused the request", detail)
		return refuse(p)
	case errors.Is(err, shipping.ErrInvalidRequest):
		return refuse(problem(http.StatusUnprocessableEntity, codeCarrierRefused, "The carrier would refuse the request",
			strings.TrimPrefix(err.Error(), "shipping: ")))
	case errors.Is(err, shipping.ErrUnauthorized), errors.Is(err, errCarrierWithheld), errors.Is(err, errCarrierKeysUnreadable):
		return carrierUnavailable(err)
	}
	return refuse(problem(http.StatusServiceUnavailable, codeCarrierUnreachable, "The carrier did not answer", what))
}

// carrierAllowed reports whether SHIPPING_CARRIERS allows carrier.
func (s *server) carrierAllowed(carrier string) bool {
	return s.deps.Config.ShippingCarrierAllowed(carrier)
}

// allowedCarriers are the adapters SHIPPING_CARRIERS allows.
func (s *server) allowedCarriers() []string {
	if s.deps.Config == nil {
		return []string{}
	}
	return append([]string{}, s.deps.Config.ShippingCarriers...)
}

// carrierServices are a carrier's products, which need no keys to read.
func carrierServices(carrier string) []shipping.Service {
	if carrier == carrierBring {
		return bring.Services()
	}
	return nil
}

// serviceOf is a carrier's product by code, false when it has none.
func serviceOf(carrier, code string) (shipping.Service, bool) {
	for _, sv := range carrierServices(carrier) {
		if sv.Code == code {
			return sv, true
		}
	}
	return shipping.Service{}, false
}

// serviceName is a product's name in language, its code when unknown.
func serviceName(carrier, code, language string) string {
	sv, ok := serviceOf(carrier, code)
	switch {
	case !ok:
		return code
	case language == "en":
		return sv.NameEN
	}
	return sv.NameNB
}

// carrierName is how a carrier is named to people.
func carrierName(carrier string) string {
	if carrier == carrierBring {
		return "Bring"
	}
	return carrier
}

// sealKeys seals an account's keys.
func (s *server) sealKeys(k carrierKeys) ([]byte, error) {
	if s.deps.Secrets == nil {
		return nil, errors.New("orders: no secrets box to seal the carrier keys with")
	}
	// Two strings always encode.
	clear, _ := json.Marshal(k)
	return s.deps.Secrets.Seal(carrierCredentialPurpose, clear)
}

// openKeys opens an account's keys; any failure is errCarrierKeysUnreadable.
func (s *server) openKeys(sealed []byte) (carrierKeys, error) {
	if s.deps.Secrets == nil || len(sealed) == 0 {
		return carrierKeys{}, errCarrierKeysUnreadable
	}
	clear, err := s.deps.Secrets.Open(carrierCredentialPurpose, sealed)
	if err != nil {
		return carrierKeys{}, errCarrierKeysUnreadable
	}
	var k carrierKeys
	if json.Unmarshal(clear, &k) != nil || k.UID == "" || k.Key == "" {
		return carrierKeys{}, errCarrierKeysUnreadable
	}
	return k, nil
}

// carrierOf is the adapter for an account: refused when SHIPPING_CARRIERS
// withholds its carrier or its keys cannot be opened. It is made outside any
// transaction, and every call on it is.
func (s *server) carrierOf(a store.OrdersCarrierAccount) (shipping.Carrier, error) {
	if !s.carrierAllowed(a.Carrier) {
		return nil, errCarrierWithheld
	}
	keys, err := s.openKeys(a.Credentials)
	if err != nil {
		s.deps.Logger.Error("orders: a carrier account's keys cannot be opened; they must be entered again", "account_id", a.ID)
		return nil, err
	}
	switch a.Carrier {
	case carrierBring:
		cfg := s.deps.Config
		client := cfg.PublicBaseURL
		if client == "" {
			client = cfg.AppOrigin
		}
		c, err := bring.New(bring.Options{
			BaseURL: cfg.ShippingBringBaseURL, UID: keys.UID, Key: keys.Key, ClientURL: client,
			CustomerNumbers: a.CustomerNumbers, Test: a.Test, Transport: s.deps.HTTPTransport, Now: s.deps.Clock,
		})
		if err != nil {
			return nil, fmt.Errorf("orders: build the Bring client: %w", err)
		}
		return c, nil
	}
	return nil, errCarrierWithheld
}

// accountView is an account as the API answers it: never its keys.
func (s *server) accountView(a store.OrdersCarrierAccount) gen.OrdersCarrierAccount {
	return gen.OrdersCarrierAccount{
		Id: a.ID, Carrier: gen.OrdersCarrierAccountCarrier(a.Carrier), CarrierAllowed: s.carrierAllowed(a.Carrier), Name: a.Name,
		CustomerNumbers: append([]string{}, a.CustomerNumbers...), HasCredentials: len(a.Credentials) > 0, Test: a.Test,
		Sender: gen.OrdersCarrierSender{
			Name: a.SenderName, Line1: a.SenderAddressLine1, Line2: a.SenderAddressLine2, PostalCode: a.SenderPostalCode,
			City: a.SenderCity, Country: a.SenderCountry, Phone: a.SenderPhone, Email: a.SenderEmail,
		},
		DefaultService: a.DefaultService, Status: gen.OrdersCarrierAccountStatus(a.Status), VerifiedAt: a.VerifiedAt,
		Version: a.Version, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}

// servicesOut are a carrier's products as the API answers them.
func servicesOut(services []shipping.Service, language string) []gen.OrdersCarrierService {
	out := make([]gen.OrdersCarrierService, 0, len(services))
	for _, sv := range services {
		name := sv.NameNB
		if language == "en" {
			name = sv.NameEN
		}
		out = append(out, gen.OrdersCarrierService{
			Code: sv.Code, Name: name, NameEn: sv.NameEN, NameNb: sv.NameNB, Segment: gen.OrdersCarrierServiceSegment(sv.Segment),
			PickupPoint: sv.PickupPoint, Waybill: sv.Waybill,
		})
	}
	return out
}

// accountInput is an account body that passed.
type accountInput struct {
	carrier, name, status string
	customers             []string
	test                  bool
	keys                  *carrierKeys
	sender                gen.OrdersCarrierSender
	defaultService        *string
}

// parseAccount checks an account body field by field.
func parseAccount(b gen.OrdersCarrierAccountInput, creating bool) (accountInput, fieldErrors) {
	var f fieldErrors
	in := accountInput{carrier: string(b.Carrier), name: strings.TrimSpace(b.Name), test: b.Test, status: accountActive}
	if !b.Carrier.Valid() {
		f.add("carrier", "The carrier is bring")
	}
	if n := utf8.RuneCountInString(in.name); n < 1 || n > 100 {
		f.add("name", "A name is 1 to 100 characters")
	}
	if len(b.CustomerNumbers) < 1 || len(b.CustomerNumbers) > 5 {
		f.add("customerNumbers", "One to five customer numbers")
	}
	for i, c := range b.CustomerNumbers {
		c = strings.TrimSpace(c)
		if !customerNumberPattern.MatchString(c) {
			f.add(fmt.Sprintf("customerNumbers[%d]", i), "A customer number is 1 to 20 letters, digits, hyphens or underscores")
			continue
		}
		if slices.Contains(in.customers, c) {
			f.add(fmt.Sprintf("customerNumbers[%d]", i), "Each customer number once")
			continue
		}
		in.customers = append(in.customers, c)
	}
	if b.Credentials != nil {
		k := carrierKeys{UID: strings.TrimSpace(b.Credentials.Uid), Key: strings.TrimSpace(b.Credentials.Key)}
		if k.UID == "" || utf8.RuneCountInString(k.UID) > 254 {
			f.add("credentials.uid", "The Mybring user id is 1 to 254 characters")
		}
		if k.Key == "" || utf8.RuneCountInString(k.Key) > 500 {
			f.add("credentials.key", "The API key is 1 to 500 characters")
		}
		in.keys = &k
	} else if creating {
		f.add("credentials", "Required: the Mybring user id and API key")
	}
	if b.Status != nil {
		if !b.Status.Valid() {
			f.add("status", "active or archived")
		}
		in.status = string(*b.Status)
	}
	if b.Sender != nil {
		in.sender = trimSender(*b.Sender, &f)
	}
	if b.DefaultService != nil && strings.TrimSpace(*b.DefaultService) != "" {
		code := strings.TrimSpace(*b.DefaultService)
		if _, ok := serviceOf(in.carrier, code); !ok {
			f.addCoded("defaultService", codeServiceUnknown, fmt.Sprintf("%q is not one of the carrier's services", code))
		}
		in.defaultService = &code
	}
	return in, f
}

// trimSender trims a sender's fields, empty to nil, and checks their lengths.
func trimSender(in gen.OrdersCarrierSender, f *fieldErrors) gen.OrdersCarrierSender {
	field := func(name string, v *string, maxLen int) *string {
		if v == nil {
			return nil
		}
		t := strings.TrimSpace(*v)
		if t == "" {
			return nil
		}
		if utf8.RuneCountInString(t) > maxLen {
			f.add("sender."+name, fmt.Sprintf("At most %d characters", maxLen))
		}
		return &t
	}
	out := gen.OrdersCarrierSender{
		Name: field("name", in.Name, 200), Line1: field("line1", in.Line1, 200), Line2: field("line2", in.Line2, 200),
		PostalCode: field("postalCode", in.PostalCode, 20), City: field("city", in.City, 100),
		Country: field("country", in.Country, 2), Phone: field("phone", in.Phone, 50), Email: field("email", in.Email, 254),
	}
	if out.Country != nil {
		c := strings.ToUpper(*out.Country)
		if len(c) != 2 {
			f.add("sender.country", "A country is two letters (ISO 3166-1)")
		}
		out.Country = &c
	}
	return out
}

// nameTaken is the 409 of an account name in use.
func nameTaken() error {
	return conflict(codeCarrierNameTaken, "The name is taken", "Another active carrier account has that name.")
}

// ListCarrierAccounts List carrier accounts
// (GET /api/v1/orders/carrier-accounts)
func (s *server) ListCarrierAccounts(ctx context.Context, _ gen.ListCarrierAccountsRequestObject) (gen.ListCarrierAccountsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListCarrierAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders: list the carrier accounts: %w", err)
	}
	out := gen.OrdersCarrierAccountList{Items: make([]gen.OrdersCarrierAccount, 0, len(rows)), Carriers: s.allowedCarriers()}
	for _, a := range rows {
		out.Items = append(out.Items, s.accountView(a))
	}
	return gen.ListCarrierAccounts200JSONResponse(out), nil
}

// GetCarrierAccount Get a carrier account
// (GET /api/v1/orders/carrier-accounts/{id})
func (s *server) GetCarrierAccount(ctx context.Context, req gen.GetCarrierAccountRequestObject) (gen.GetCarrierAccountResponseObject, error) {
	a, err := store.New(s.deps.Pool).GetCarrierAccount(ctx, req.Id)
	if noRows(err) {
		return gen.GetCarrierAccount404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read carrier account %d: %w", req.Id, err)
	}
	return gen.GetCarrierAccount200JSONResponse(s.accountView(a)), nil
}

// CreateCarrierAccount Add a carrier account
// (POST /api/v1/orders/carrier-accounts)
func (s *server) CreateCarrierAccount(ctx context.Context, req gen.CreateCarrierAccountRequestObject) (gen.CreateCarrierAccountResponseObject, error) {
	resp := answers[gen.CreateCarrierAccountResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.CreateCarrierAccountResponseObject {
			return gen.CreateCarrierAccount400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.OrdersProblem) gen.CreateCarrierAccountResponseObject {
			return gen.CreateCarrierAccount409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.CreateCarrierAccountResponseObject {
			return gen.CreateCarrierAccount503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, carrierAccountInputSchema)
	in, pf := parseAccount(*req.Body, true)
	for field, msgs := range pf.errs {
		for _, m := range msgs {
			f.add(field, m)
		}
	}
	if f.code == "" {
		f.code = pf.code
	}
	if f.failed() {
		return gen.CreateCarrierAccount400ApplicationProblemPlusJSONResponse(invalid(invalidCarrierTitle, f)), nil
	}
	if !s.carrierAllowed(in.carrier) {
		return answer(carrierUnavailable(errCarrierWithheld), resp)
	}
	sealed, err := s.sealKeys(*in.keys)
	if err != nil {
		return nil, err
	}
	row, err := store.New(s.deps.Pool).InsertCarrierAccount(ctx, store.InsertCarrierAccountParams{
		Carrier: in.carrier, Name: in.name, CustomerNumbers: in.customers, Credentials: sealed, Test: in.test,
		SenderName: in.sender.Name, SenderAddressLine1: in.sender.Line1, SenderAddressLine2: in.sender.Line2,
		SenderPostalCode: in.sender.PostalCode, SenderCity: in.sender.City, SenderCountry: in.sender.Country,
		SenderPhone: in.sender.Phone, SenderEmail: in.sender.Email, DefaultService: in.defaultService, Now: s.deps.Clock(),
	})
	if db.IsUniqueViolation(err, "ux_carrier_accounts_name") {
		return answer(nameTaken(), resp)
	}
	if err != nil {
		return nil, fmt.Errorf("orders: add a carrier account: %w", err)
	}
	return gen.CreateCarrierAccount201JSONResponse(s.accountView(row)), nil
}

// UpdateCarrierAccount Save a carrier account
// (PUT /api/v1/orders/carrier-accounts/{id})
//
// The row is read under its lock; absent credentials keep the stored sealed
// bytes as they are, given ones replace them and clear verified_at.
func (s *server) UpdateCarrierAccount(ctx context.Context, req gen.UpdateCarrierAccountRequestObject) (gen.UpdateCarrierAccountResponseObject, error) {
	resp := answers[gen.UpdateCarrierAccountResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.UpdateCarrierAccountResponseObject {
			return gen.UpdateCarrierAccount400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateCarrierAccountResponseObject { return gen.UpdateCarrierAccount404Response{} },
		conflict: func(p gen.OrdersProblem) gen.UpdateCarrierAccountResponseObject {
			return gen.UpdateCarrierAccount409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.UpdateCarrierAccountResponseObject {
			return gen.UpdateCarrierAccount503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, carrierAccountInputSchema)
	in, pf := parseAccount(*req.Body, false)
	for field, msgs := range pf.errs {
		for _, m := range msgs {
			f.add(field, m)
		}
	}
	if f.code == "" {
		f.code = pf.code
	}
	if req.Body.Version == nil {
		f.add("version", "Required: the version the client read")
	}
	if f.failed() {
		return gen.UpdateCarrierAccount400ApplicationProblemPlusJSONResponse(invalid(invalidCarrierTitle, f)), nil
	}
	var sealed []byte
	if in.keys != nil {
		var err error
		if sealed, err = s.sealKeys(*in.keys); err != nil {
			return nil, err
		}
	}
	var out store.OrdersCarrierAccount
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		cur, err := txq.LockCarrierAccount(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("orders: lock carrier account %d: %w", req.Id, err)
		}
		if cur.Version != *req.Body.Version {
			return versionConflict("carrier account's version", cur.Version, *req.Body.Version)
		}
		if cur.Carrier != in.carrier {
			return refuseField(invalidCarrierTitle, "carrier", "", "An account's carrier cannot change; add another account instead")
		}
		keys := cur.Credentials
		if sealed != nil {
			keys = sealed
		}
		out, err = txq.UpdateCarrierAccount(ctx, store.UpdateCarrierAccountParams{
			ID: cur.ID, Name: in.name, CustomerNumbers: in.customers, Credentials: keys, Test: in.test,
			SenderName: in.sender.Name, SenderAddressLine1: in.sender.Line1, SenderAddressLine2: in.sender.Line2,
			SenderPostalCode: in.sender.PostalCode, SenderCity: in.sender.City, SenderCountry: in.sender.Country,
			SenderPhone: in.sender.Phone, SenderEmail: in.sender.Email, DefaultService: in.defaultService, Status: in.status,
			KeysChanged: sealed != nil || !slices.Equal(cur.CustomerNumbers, in.customers) || cur.Test != in.test, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, "ux_carrier_accounts_name") {
			return nameTaken()
		}
		if err != nil {
			return fmt.Errorf("orders: save carrier account %d: %w", cur.ID, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.UpdateCarrierAccount200JSONResponse(s.accountView(out)), nil
}

// VerifyCarrierAccount Verify a carrier account
// (POST /api/v1/orders/carrier-accounts/{id}/verify)
func (s *server) VerifyCarrierAccount(ctx context.Context, req gen.VerifyCarrierAccountRequestObject) (gen.VerifyCarrierAccountResponseObject, error) {
	a, err := store.New(s.deps.Pool).GetCarrierAccount(ctx, req.Id)
	if noRows(err) {
		return gen.VerifyCarrierAccount404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read carrier account %d: %w", req.Id, err)
	}
	c, err := s.carrierOf(a)
	if err != nil {
		if errors.Is(err, errCarrierWithheld) || errors.Is(err, errCarrierKeysUnreadable) {
			_, p, _ := answerOf(carrierUnavailable(err))
			return gen.VerifyCarrierAccount503ApplicationProblemPlusJSONResponse(p), nil
		}
		return nil, err
	}
	out := gen.OrdersCarrierVerification{Result: gen.Ok, Notes: []string{}, Services: []gen.OrdersCarrierService{}}
	v, err := c.Verify(ctx)
	var refusal *shipping.CarrierRefusal
	switch {
	case err == nil:
		out.PricesAvailable = v.PricesAvailable
		out.Notes = append(out.Notes, v.Notes...)
		if err := store.New(s.deps.Pool).SetCarrierAccountVerified(ctx, store.SetCarrierAccountVerifiedParams{
			ID: a.ID, Version: a.Version, Now: ptr(s.deps.Clock()),
		}); err != nil {
			return nil, fmt.Errorf("orders: record carrier account %d verified: %w", a.ID, err)
		}
	case errors.Is(err, shipping.ErrUnauthorized):
		out.Result, out.Message = gen.Unauthorized, ptr("The carrier refused the keys: check the Mybring user id and API key.")
	case errors.As(err, &refusal):
		out.Result, out.Message = gen.Refused, ptr(refusal.Message)
	default:
		s.deps.Logger.WarnContext(ctx, "orders: a carrier account could not be verified", "account_id", a.ID, "error", err.Error())
		out.Result, out.Message = gen.Unreachable, ptr("The carrier did not answer. Try again later.")
	}
	if out.Result == gen.Ok {
		services, err := c.Services(ctx)
		if err != nil {
			return nil, fmt.Errorf("orders: read carrier account %d's services: %w", a.ID, err)
		}
		out.Services = servicesOut(services, "en")
	}
	return gen.VerifyCarrierAccount200JSONResponse(out), nil
}

// ListCarrierServices List a carrier account's services
// (GET /api/v1/orders/carrier-accounts/{id}/services)
func (s *server) ListCarrierServices(ctx context.Context, req gen.ListCarrierServicesRequestObject) (gen.ListCarrierServicesResponseObject, error) {
	a, err := store.New(s.deps.Pool).GetCarrierAccount(ctx, req.Id)
	if noRows(err) {
		return gen.ListCarrierServices404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read carrier account %d: %w", req.Id, err)
	}
	return gen.ListCarrierServices200JSONResponse(gen.OrdersCarrierServiceList{
		Items: servicesOut(carrierServices(a.Carrier), "en"),
	}), nil
}

// ListPickupPoints List pickup points
// (GET /api/v1/orders/carrier-accounts/{id}/pickup-points)
func (s *server) ListPickupPoints(ctx context.Context, req gen.ListPickupPointsRequestObject) (gen.ListPickupPointsResponseObject, error) {
	resp := answers[gen.ListPickupPointsResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.ListPickupPointsResponseObject {
			return gen.ListPickupPoints400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.ListPickupPointsResponseObject { return gen.ListPickupPoints404Response{} },
		unprocessable: func(p gen.OrdersProblem) gen.ListPickupPointsResponseObject {
			return gen.ListPickupPoints422ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.ListPickupPointsResponseObject {
			return gen.ListPickupPoints503ApplicationProblemPlusJSONResponse(p)
		},
	}
	a, err := store.New(s.deps.Pool).GetCarrierAccount(ctx, req.Id)
	if noRows(err) {
		return gen.ListPickupPoints404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read carrier account %d: %w", req.Id, err)
	}
	c, err := s.carrierOf(a)
	if err != nil {
		return answer(carrierFailed(err, ""), resp)
	}
	out := gen.OrdersPickupPointList{Items: []gen.OrdersPickupPoint{}}
	if !c.Capabilities().PickupPoints {
		return gen.ListPickupPoints200JSONResponse(out), nil
	}
	country := "NO"
	if req.Params.Country != nil {
		country = strings.ToUpper(*req.Params.Country)
	}
	points, err := c.PickupPoints(ctx, shipping.Address{PostalCode: strings.TrimSpace(req.Params.PostalCode), Country: country})
	if err != nil {
		return answer(carrierFailed(err, "The carrier did not answer with its pickup points. Try again later."), resp)
	}
	for _, p := range points {
		out.Items = append(out.Items, gen.OrdersPickupPoint{
			Id: p.ID, Name: p.Name, Address: p.Address, PostalCode: p.PostalCode, City: p.City, Locker: p.Locker, OpeningHours: p.OpeningHours,
		})
	}
	return gen.ListPickupPoints200JSONResponse(out), nil
}

// GetSiteCarrierDefaults Get the default carrier accounts
// (GET /api/v1/orders/shipping/site-defaults)
func (s *server) GetSiteCarrierDefaults(ctx context.Context, _ gen.GetSiteCarrierDefaultsRequestObject) (gen.GetSiteCarrierDefaultsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListSiteCarrierDefaults(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders: read the default carrier accounts: %w", err)
	}
	return gen.GetSiteCarrierDefaults200JSONResponse(siteDefaultsOut(rows)), nil
}

func siteDefaultsOut(rows []store.OrdersSiteCarrierDefault) gen.OrdersSiteCarrierDefaults {
	out := gen.OrdersSiteCarrierDefaults{Items: make([]gen.OrdersSiteCarrierDefault, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, gen.OrdersSiteCarrierDefault{SiteId: r.SiteID, AccountId: r.AccountID})
	}
	return out
}

// PutSiteCarrierDefaults Save the default carrier accounts
// (PUT /api/v1/orders/shipping/site-defaults)
//
// Each site is one Inventory knows (judged through contracts.Stock before the
// transaction), named once, with an active account.
func (s *server) PutSiteCarrierDefaults(ctx context.Context, req gen.PutSiteCarrierDefaultsRequestObject) (gen.PutSiteCarrierDefaultsResponseObject, error) {
	f := s.unknownProperties(ctx, siteDefaultsSchema)
	seen := map[int64]bool{}
	for i, d := range req.Body.Items {
		field := fmt.Sprintf("items[%d]", i)
		if seen[d.SiteId] {
			f.add(field+".siteId", "Each site once")
			continue
		}
		seen[d.SiteId] = true
		if err := s.judgeSite(ctx, &f, field+".siteId", d.SiteId); err != nil {
			return nil, err
		}
	}
	if f.failed() {
		return gen.PutSiteCarrierDefaults400ApplicationProblemPlusJSONResponse(invalid(invalidCarrierTitle, f)), nil
	}
	var rows []store.OrdersSiteCarrierDefault
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var af fieldErrors
		for i, d := range req.Body.Items {
			a, err := txq.GetCarrierAccount(ctx, d.AccountId)
			if noRows(err) || (err == nil && a.Status != accountActive) {
				af.add(fmt.Sprintf("items[%d].accountId", i), "No active carrier account has that id")
				continue
			}
			if err != nil {
				return fmt.Errorf("orders: read carrier account %d: %w", d.AccountId, err)
			}
		}
		if af.failed() {
			return refuseInvalid(invalidCarrierTitle, af)
		}
		if err := txq.DeleteSiteCarrierDefaults(ctx); err != nil {
			return fmt.Errorf("orders: clear the default carrier accounts: %w", err)
		}
		now := s.deps.Clock()
		for _, d := range req.Body.Items {
			if err := txq.InsertSiteCarrierDefault(ctx, store.InsertSiteCarrierDefaultParams{SiteID: d.SiteId, AccountID: d.AccountId, Now: now}); err != nil {
				return fmt.Errorf("orders: save site %d's default carrier account: %w", d.SiteId, err)
			}
		}
		var err error
		rows, err = txq.ListSiteCarrierDefaults(ctx)
		return err
	})
	if err != nil {
		return answer(err, answers[gen.PutSiteCarrierDefaultsResponseObject]{
			badRequest: func(p gen.OrdersProblem) gen.PutSiteCarrierDefaultsResponseObject {
				return gen.PutSiteCarrierDefaults400ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.PutSiteCarrierDefaults200JSONResponse(siteDefaultsOut(rows)), nil
}

// defaultAccount is the carrier account a site uses: the site's default,
// else the only active account; nil when there is none to choose.
func defaultAccount(ctx context.Context, q *store.Queries, siteID *int64) (*store.OrdersCarrierAccount, error) {
	if siteID != nil {
		id, err := q.SiteCarrierDefault(ctx, *siteID)
		switch {
		case err == nil:
			a, err := q.GetCarrierAccount(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("orders: read carrier account %d: %w", id, err)
			}
			if a.Status == accountActive {
				return &a, nil
			}
		case !noRows(err):
			return nil, fmt.Errorf("orders: read site %d's default carrier account: %w", *siteID, err)
		}
	}
	rows, err := q.ListCarrierAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders: list the carrier accounts: %w", err)
	}
	var active []store.OrdersCarrierAccount
	for _, a := range rows {
		if a.Status == accountActive {
			active = append(active, a)
		}
	}
	if len(active) == 1 {
		return &active[0], nil
	}
	return nil, nil
}

// noCarrierAccount is the 409 of a shipment or an estimate with no account.
func noCarrierAccount() error {
	return conflict(codeNoCarrierAccount, "No carrier account",
		"No carrier account is the site's default, and there is not exactly one active account. Add one, or name the site's default, under Integrations › Shipping.")
}
