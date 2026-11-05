package bring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/shipping"
)

// This file is the Shipping Guide API v2 (POST /shippingguide/api/v2/products):
// the options and prices for a parcel, and Verify. It takes packages in
// grams and centimetres, and answers listPrice — and netPrice when the
// Mybring user has the financial role on the customer number — each with and
// without VAT. Both are estimates: the invoice is the truth.

// guideDate is the Shipping Guide's date: year, month and day as strings.
type guideDate struct {
	Year  string `json:"year"`
	Month string `json:"month"`
	Day   string `json:"day"`
}

func guideDateOf(t time.Time) guideDate {
	return guideDate{Year: strconv.Itoa(t.Year()), Month: fmt.Sprintf("%02d", int(t.Month())), Day: fmt.Sprintf("%02d", t.Day())}
}

func (d guideDate) time() (time.Time, bool) {
	y, e1 := strconv.Atoi(d.Year)
	m, e2 := strconv.Atoi(d.Month)
	day, e3 := strconv.Atoi(d.Day)
	if e1 != nil || e2 != nil || e3 != nil || m < 1 || m > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	return time.Date(y, time.Month(m), day, 0, 0, 0, 0, time.UTC), true
}

type guidePackage struct {
	ID          string `json:"id"`
	GrossWeight int64  `json:"grossWeight"`
	Length      int64  `json:"length,omitempty"`
	Width       int64  `json:"width,omitempty"`
	Height      int64  `json:"height,omitempty"`
}

type guideProduct struct {
	ID             string `json:"id"`
	CustomerNumber string `json:"customerNumber"`
}

type guideConsignment struct {
	ID              string         `json:"id"`
	FromCountryCode string         `json:"fromCountryCode"`
	FromPostalCode  string         `json:"fromPostalCode"`
	ToCountryCode   string         `json:"toCountryCode"`
	ToPostalCode    string         `json:"toPostalCode"`
	ShippingDate    guideDate      `json:"shippingDate"`
	Packages        []guidePackage `json:"packages"`
	Products        []guideProduct `json:"products"`
}

type guideRequest struct {
	Consignments         []guideConsignment `json:"consignments"`
	WithPrice            bool               `json:"withPrice"`
	WithExpectedDelivery bool               `json:"withExpectedDelivery"`
	WithGUIInformation   bool               `json:"withGuiInformation"`
	Language             string             `json:"language"`
}

// amount is a decimal Bring answers as a string or a number.
type amount string

func (a *amount) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*a = amount(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*a = amount(n.String())
	return nil
}

// minor is the decimal in minor units, rounded half away from zero; false
// when it is empty or does not parse.
func (a amount) minor() (int64, bool) {
	s := strings.TrimSpace(string(a))
	if s == "" {
		return 0, false
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > math.MaxInt64/1000 {
		return 0, false
	}
	frac += "000"
	f, err := strconv.ParseInt(frac[:3], 10, 64)
	if err != nil || strings.Trim(frac, "0123456789") != "" {
		return 0, false
	}
	m := w*100 + f/10
	if f%10 >= 5 {
		m++
	}
	if neg {
		m = -m
	}
	return m, true
}

type guidePrice struct {
	CurrencyCode                   string `json:"currencyCode"`
	PriceWithoutAdditionalServices *struct {
		AmountWithoutVAT amount `json:"amountWithoutVAT"`
		VAT              amount `json:"vat"`
		AmountWithVAT    amount `json:"amountWithVAT"`
	} `json:"priceWithoutAdditionalServices"`
}

// money is the price without VAT and its VAT; false when it is absent or
// does not parse.
func (p *guidePrice) money() (price, vat shipping.Money, ok bool) {
	if p == nil || p.PriceWithoutAdditionalServices == nil || p.CurrencyCode == "" {
		return price, vat, false
	}
	net, ok1 := p.PriceWithoutAdditionalServices.AmountWithoutVAT.minor()
	tax, ok2 := p.PriceWithoutAdditionalServices.VAT.minor()
	if !ok1 {
		return price, vat, false
	}
	if !ok2 {
		if gross, ok3 := p.PriceWithoutAdditionalServices.AmountWithVAT.minor(); ok3 {
			tax = gross - net
		}
	}
	return shipping.Money{Currency: p.CurrencyCode, Minor: net}, shipping.Money{Currency: p.CurrencyCode, Minor: tax}, true
}

type guideAnswerProduct struct {
	ID             string `json:"id"`
	GUIInformation *struct {
		DisplayName string `json:"displayName"`
		ProductName string `json:"productName"`
	} `json:"guiInformation"`
	Price *struct {
		ListPrice *guidePrice `json:"listPrice"`
		NetPrice  *guidePrice `json:"netPrice"`
	} `json:"price"`
	ExpectedDelivery *struct {
		ExpectedDeliveryDate *guideDate `json:"expectedDeliveryDate"`
	} `json:"expectedDelivery"`
	Errors []bringError `json:"errors"`
}

type guideAnswer struct {
	Consignments []struct {
		Products []guideAnswerProduct `json:"products"`
	} `json:"consignments"`
	Errors []bringError `json:"errors"`
}

// cm is millimetres as whole centimetres, rounded up; 0 stays 0.
func cm(mm int64) int64 { return (mm + 9) / 10 }

// checkPackages refuses packages Bring would refuse: none, or one without a
// weight.
func checkPackages(pkgs []shipping.Package) error {
	if len(pkgs) == 0 {
		return fmt.Errorf("%w: at least one package is required", shipping.ErrInvalidRequest)
	}
	for i, p := range pkgs {
		if p.WeightGrams <= 0 || p.LengthMM < 0 || p.WidthMM < 0 || p.HeightMM < 0 {
			return fmt.Errorf("%w: package %d needs a weight above 0 and no negative dimension", shipping.ErrInvalidRequest, i+1)
		}
	}
	return nil
}

// guide asks the Shipping Guide for codes between two addresses.
func (c *Client) guide(ctx context.Context, from, to shipping.Address, pkgs []shipping.Package, codes []string, day time.Time) (guideAnswer, error) {
	gc := guideConsignment{
		ID: "1", FromCountryCode: countryOr(from.Country), FromPostalCode: from.PostalCode,
		ToCountryCode: countryOr(to.Country), ToPostalCode: to.PostalCode, ShippingDate: guideDateOf(day),
	}
	for i, p := range pkgs {
		gc.Packages = append(gc.Packages, guidePackage{
			ID: strconv.Itoa(i + 1), GrossWeight: p.WeightGrams, Length: cm(p.LengthMM), Width: cm(p.WidthMM), Height: cm(p.HeightMM),
		})
	}
	for _, code := range codes {
		gc.Products = append(gc.Products, guideProduct{ID: code, CustomerNumber: c.customerFor(code)})
	}
	a, err := c.call(ctx, "shipping guide", http.MethodPost, c.endpoint(pathShippingGuide, nil), guideRequest{
		Consignments: []guideConsignment{gc}, WithPrice: true, WithExpectedDelivery: true, WithGUIInformation: true, Language: "en",
	})
	if err != nil {
		return guideAnswer{}, err
	}
	if err := classify("shipping guide", a); err != nil {
		return guideAnswer{}, err
	}
	var out guideAnswer
	if err := json.Unmarshal(a.body, &out); err != nil {
		return guideAnswer{}, errors.New("bring: the Shipping Guide's answer does not decode")
	}
	if len(out.Errors) > 0 {
		return guideAnswer{}, &shipping.CarrierRefusal{Code: out.Errors[0].code(), Message: out.Errors[0].message()}
	}
	return out, nil
}

func countryOr(c string) string {
	if c == "" {
		return "NO"
	}
	return strings.ToUpper(c)
}

// Rates asks the Shipping Guide for r's products (every product of the data
// file when r names none): one option per product Bring prices. The net price
// is preferred, else the list price; both are marked Estimate. A product
// Bring answers with an error is left out; when every one is, the first
// error is the refusal.
func (c *Client) Rates(ctx context.Context, r shipping.RateRequest) ([]shipping.Option, error) {
	if err := checkPackages(r.Packages); err != nil {
		return nil, err
	}
	if r.From.PostalCode == "" || r.To.PostalCode == "" {
		return nil, fmt.Errorf("%w: both postal codes are required", shipping.ErrInvalidRequest)
	}
	codes := r.Services
	if len(codes) == 0 {
		for _, p := range products {
			codes = append(codes, p.Code)
		}
	}
	day := r.ShippingDate
	if day.IsZero() {
		day = c.now()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ans, err := c.guide(ctx, r.From, r.To, r.Packages, codes, day)
	if err != nil {
		return nil, err
	}
	var out []shipping.Option
	var firstErr *bringError
	for _, cons := range ans.Consignments {
		for _, p := range cons.Products {
			if len(p.Errors) > 0 {
				if firstErr == nil {
					firstErr = &p.Errors[0]
				}
				continue
			}
			o := shipping.Option{Service: p.ID, Estimate: true}
			if p.GUIInformation != nil {
				o.Name = p.GUIInformation.DisplayName
			}
			if o.Name == "" {
				if d, ok := productOf(p.ID); ok {
					o.Name = d.NameEN
				}
			}
			if p.Price != nil {
				price, vat, ok := p.Price.NetPrice.money()
				if !ok {
					price, vat, ok = p.Price.ListPrice.money()
				}
				if !ok {
					continue
				}
				o.Price, o.VAT = price, vat
			} else {
				continue
			}
			if p.ExpectedDelivery != nil && p.ExpectedDelivery.ExpectedDeliveryDate != nil {
				if t, ok := p.ExpectedDelivery.ExpectedDeliveryDate.time(); ok {
					o.ExpectedDate = &t
				}
			}
			out = append(out, o)
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, &shipping.CarrierRefusal{Code: firstErr.code(), Message: firstErr.message()}
	}
	return out, nil
}

// Verify asks the Shipping Guide for one product between two Oslo postal
// codes on the account's first customer number: refused keys are
// ErrUnauthorized, a customer number the user may not use Bring's refusal,
// and an answer with a list price but no net price says the user lacks the
// financial role, so prices are list prices only.
func (c *Client) Verify(ctx context.Context) (shipping.Verification, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	probe := shipping.Address{PostalCode: "0150", Country: "NO"}
	code := "5000"
	ans, err := c.guide(ctx, probe, shipping.Address{PostalCode: "0484", Country: "NO"},
		[]shipping.Package{{WeightGrams: 1000}}, []string{code}, c.now())
	if err != nil {
		return shipping.Verification{}, err
	}
	for _, cons := range ans.Consignments {
		for _, p := range cons.Products {
			if len(p.Errors) > 0 {
				return shipping.Verification{}, &shipping.CarrierRefusal{Code: p.Errors[0].code(), Message: p.Errors[0].message()}
			}
			if p.Price == nil {
				continue
			}
			if _, _, ok := p.Price.NetPrice.money(); ok {
				return shipping.Verification{PricesAvailable: true}, nil
			}
		}
	}
	return shipping.Verification{PricesAvailable: false, Notes: []string{
		"Bring answered no agreement price: the Mybring user needs the financial role on the customer number. List prices are used meanwhile.",
	}}, nil
}
