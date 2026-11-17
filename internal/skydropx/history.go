package skydropx

import (
	"context"
	"sort"
	"strings"
	"unicode"
)

// PastIssue is a customer's past shipment that went wrong: they didn't pay (returned or not collected)
// or were slow to pick up the package at the carrier's office.
type PastIssue struct {
	CODShipment
	Kind         string // no_pago | oficina
	DaysAtOffice int    // only for "oficina"
}

// maxHistoryTracks limits how many of a customer's delivered shipments are tracked to measure office time.
const maxHistoryTracks = 8

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DocumentKey normalizes an ID document for comparison (digits only; "" if too short to be one).
func DocumentKey(s string) string {
	if d := onlyDigits(s); len(d) >= 5 {
		return d
	}
	return ""
}

// PhoneKey normalizes a phone for comparison: the last 10 digits ("" if it has fewer than 7).
func PhoneKey(s string) string {
	d := onlyDigits(s)
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	if len(d) < 7 {
		return ""
	}
	return d
}

// CustomerHistory finds past shipments with that ID document or phone and returns the ones that
// went wrong: those not collected and those that waited at the office officeDays days or more before
// the customer picked them up. It also returns how many past shipments it found in total.
// Shipments to collect don't count: it isn't known yet how they end.
func (c *Client) CustomerHistory(ctx context.Context, idNumber, phoneNum string, officeDays int) (badOnes []PastIssue, total int, err error) {
	doc, tel := DocumentKey(idNumber), PhoneKey(phoneNum)
	if doc == "" && tel == "" {
		return nil, 0, nil
	}
	shps, err := c.CODShipments(ctx)
	if err != nil {
		return nil, 0, err
	}
	var theirs []CODShipment
	for _, e := range shps {
		if e.Status == "cancelled" || e.Tracking == "" {
			continue
		}
		if (doc != "" && DocumentKey(e.TaxID) == doc) || (tel != "" && PhoneKey(e.Phone) == tel) {
			theirs = append(theirs, e)
		}
	}
	sort.SliceStable(theirs, func(i, j int) bool { return theirs[i].Date > theirs[j].Date }) // newest first

	tracked := 0
	for _, e := range theirs {
		switch e.Status {
		case "failed":
			badOnes = append(badOnes, PastIssue{CODShipment: e, Kind: "no_pago"})
		case "collected":
			if tracked >= maxHistoryTracks || ctx.Err() != nil {
				continue
			}
			tracked++
			st, err := c.TrackStatus(ctx, e.Tracking, e.Carrier)
			if err == nil && st.DaysAtOffice >= officeDays {
				badOnes = append(badOnes, PastIssue{CODShipment: e, Kind: "oficina", DaysAtOffice: st.DaysAtOffice})
			}
		}
	}
	return badOnes, len(theirs), nil
}
