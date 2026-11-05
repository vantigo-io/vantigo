package bring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/shipping"
)

// This file is the Tracking API (GET /tracking/api/v2/tracking.json?q=…), which
// takes a consignment number, a package number or a reference, and the
// Pickup Point API.

// fold maps Bring's event statuses onto the port's states; an unknown status
// is in transit, its code kept.
func fold(status string) shipping.State {
	switch strings.ToUpper(status) {
	case "PRE_NOTIFIED", "NOTIFICATION_SENT", "BOOKED":
		return shipping.StateBooked
	case "READY_FOR_PICKUP":
		return shipping.StateReadyForPickup
	case "DELIVERED":
		return shipping.StateDelivered
	case "RETURN", "DELIVERED_SENDER", "RETURNED":
		return shipping.StateReturned
	case "DEVIATION", "DAMAGED", "STOPPED":
		return shipping.StateException
	case "CANCELLED":
		return shipping.StateCancelled
	}
	return shipping.StateInTransit
}

type trackEvent struct {
	Status      string `json:"status"`
	Description string `json:"description"`
	DateIso     string `json:"dateIso"`
	City        string `json:"city"`
	PostalCode  string `json:"postalCode"`
	Country     string `json:"country"`
}

type trackAnswer struct {
	ConsignmentSet []struct {
		ConsignmentID string `json:"consignmentId"`
		PackageSet    []struct {
			PackageNumber string       `json:"packageNumber"`
			EventSet      []trackEvent `json:"eventSet"`
		} `json:"packageSet"`
		Error *struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"consignmentSet"`
}

// Track asks the Tracking API for each reference in turn, under one
// deadline: Bring's q takes one. A reference Bring does not know is left out.
func (c *Client) Track(ctx context.Context, refs []string) ([]shipping.TrackingStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var out []shipping.TrackingStatus
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			continue
		}
		st, err := c.track(ctx, ref)
		if err != nil {
			return out, err
		}
		if st != nil {
			out = append(out, *st)
		}
	}
	return out, nil
}

// track is one reference's tracking, nil when Bring knows none.
func (c *Client) track(ctx context.Context, ref string) (*shipping.TrackingStatus, error) {
	a, err := c.call(ctx, "tracking", http.MethodGet, c.endpoint(pathTracking, url.Values{"q": {ref}}), nil)
	if err != nil {
		return nil, err
	}
	if a.status == http.StatusNotFound {
		return nil, nil
	}
	if err := classify("tracking", a); err != nil {
		return nil, err
	}
	var ans trackAnswer
	if err := json.Unmarshal(a.body, &ans); err != nil {
		return nil, errors.New("bring: the tracking answer does not decode")
	}
	for _, cs := range ans.ConsignmentSet {
		if cs.Error != nil || cs.ConsignmentID == "" {
			continue
		}
		st := &shipping.TrackingStatus{Reference: ref, ConsignmentNumber: cs.ConsignmentID, State: shipping.StateBooked,
			TrackingURL: trackingPage + cs.ConsignmentID}
		seen := map[string]bool{}
		for _, p := range cs.PackageSet {
			if p.PackageNumber != "" {
				st.PackageNumbers = append(st.PackageNumbers, p.PackageNumber)
			}
			for _, e := range p.EventSet {
				at, err := time.Parse(time.RFC3339, e.DateIso)
				if err != nil {
					continue
				}
				key := e.Status + "|" + e.DateIso + "|" + e.Description
				if seen[key] {
					continue
				}
				seen[key] = true
				place := strings.TrimSpace(strings.Join(slices.DeleteFunc([]string{e.PostalCode, e.City}, func(s string) bool { return s == "" }), " "))
				st.Events = append(st.Events, shipping.TrackingEvent{
					State: fold(e.Status), Code: e.Status, Text: e.Description, Place: place, At: at,
				})
			}
		}
		slices.SortStableFunc(st.Events, func(x, y shipping.TrackingEvent) int { return x.At.Compare(y.At) })
		if n := len(st.Events); n > 0 {
			st.State = st.Events[n-1].State
		}
		return st, nil
	}
	return nil, nil
}

type pickupAnswer struct {
	PickupPoint []struct {
		ID                  any    `json:"id"`
		Name                string `json:"name"`
		Address             string `json:"address"`
		PostalCode          string `json:"postalCode"`
		City                string `json:"city"`
		CountryCode         string `json:"countryCode"`
		OpeningHoursEnglish string `json:"openingHoursEnglish"`
		Type                any    `json:"type"`
		Locker              bool   `json:"locker"`
		MaxWeightKg         amount `json:"maxWeight"`
	} `json:"pickupPoint"`
}

// PickupPoints are the pickup points Bring lists for a postal code.
func (c *Client) PickupPoints(ctx context.Context, near shipping.Address) ([]shipping.PickupPoint, error) {
	if strings.TrimSpace(near.PostalCode) == "" {
		return nil, fmt.Errorf("%w: a postal code is required", shipping.ErrInvalidRequest)
	}
	country := countryOr(near.Country)
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	path := pathPickupPoints + url.PathEscape(country) + "/postalCode/" + url.PathEscape(near.PostalCode) + ".json"
	a, err := c.call(ctx, "pickup points", http.MethodGet, c.endpoint(path, nil), nil)
	if err != nil {
		return nil, err
	}
	if err := classify("pickup points", a); err != nil {
		return nil, err
	}
	var ans pickupAnswer
	if err := json.Unmarshal(a.body, &ans); err != nil {
		return nil, errors.New("bring: the pickup points' answer does not decode")
	}
	out := make([]shipping.PickupPoint, 0, len(ans.PickupPoint))
	for _, p := range ans.PickupPoint {
		pp := shipping.PickupPoint{
			ID: fmt.Sprint(p.ID), Name: p.Name, Address: p.Address, PostalCode: p.PostalCode, City: p.City,
			Country: cmpOr(p.CountryCode, country), OpeningHours: p.OpeningHoursEnglish, Locker: p.Locker,
		}
		if t := strings.ToLower(fmt.Sprint(p.Type)); strings.Contains(t, "locker") || strings.Contains(t, "boks") {
			pp.Locker = true
		}
		if g, ok := p.MaxWeightKg.minor(); ok {
			pp.MaxWeightGrams = g * 10
		}
		if p.ID == nil || pp.ID == "" {
			continue
		}
		out = append(out, pp)
	}
	return out, nil
}
