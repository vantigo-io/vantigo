package invoices

import (
	"slices"
	"testing"
	"time"
)

// The channel rule (I35, plan E3): every row of the table — person and
// business, each preference, credit notes, before and after the B2B duty, and
// installations without mail, EHF or eFaktura.
func TestChooseSendChannel(t *testing.T) {
	before := time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC)
	after := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)
	all := func(in channelInput) channelInput {
		in.mailAvailable, in.ehfAvailable, in.efakturaAvailable, in.hasInvoiceEmail = true, true, true, true
		if in.today.IsZero() {
			in.today = before
		}
		return in
	}
	tests := []struct {
		name string
		in   channelInput
		want channelPlan
	}{
		// No preference, a person.
		{"person: eFaktura, e-mail, print", all(channelInput{customerType: "person"}),
			channelPlan{channels: []string{channelEfaktura, channelEmail, channelPrint}}},
		{"person without an invoice e-mail", func() channelInput {
			in := all(channelInput{customerType: "person"})
			in.hasInvoiceEmail = false
			return in
		}(), channelPlan{channels: []string{channelEfaktura, channelPrint}}},
		{"person, eFaktura switched off", func() channelInput {
			in := all(channelInput{customerType: "person"})
			in.efakturaAvailable = false
			return in
		}(), channelPlan{channels: []string{channelEmail, channelPrint}}},
		{"person, no mail and no eFaktura", channelInput{customerType: "person", hasInvoiceEmail: true, today: before},
			channelPlan{channels: []string{channelPrint}}},
		{"person's credit note: never eFaktura", all(channelInput{customerType: "person", creditNote: true}),
			channelPlan{channels: []string{channelEmail, channelPrint}}},
		{"a person-typed customer with an org number after the duty: eFaktura, else skipped, never e-mail",
			all(channelInput{customerType: "person", norwegianBusiness: true, today: after}),
			channelPlan{channels: []string{channelEfaktura}, endState: jobSkipped, endCode: warningBuyerNorwegianBusinessRequired}},
		{"a person-typed customer with an org number before the duty", all(channelInput{customerType: "person", norwegianBusiness: true}),
			channelPlan{channels: []string{channelEfaktura, channelEmail, channelPrint}}},
		{"a credit note preferring eFaktura to an org-numbered buyer after the duty: skipped",
			all(channelInput{customerType: "person", preference: "efaktura", creditNote: true, norwegianBusiness: true, today: after}),
			channelPlan{endState: jobSkipped, endCode: warningBuyerNorwegianBusinessRequired}},
		{"unknown customer without an org number is a person", all(channelInput{}),
			channelPlan{channels: []string{channelEfaktura, channelEmail, channelPrint}}},

		// No preference, a business.
		{"business before the duty: EHF, e-mail", all(channelInput{customerType: "business", norwegianBusiness: true}),
			channelPlan{channels: []string{channelEhf, channelEmail}}},
		{"business after the duty: EHF, else skipped", all(channelInput{customerType: "business", norwegianBusiness: true, today: after}),
			channelPlan{channels: []string{channelEhf}, endState: jobSkipped, endCode: warningBuyerNorwegianBusinessRequired}},
		{"business after the duty without EHF: skipped", func() channelInput {
			in := all(channelInput{customerType: "business", norwegianBusiness: true, today: after})
			in.ehfAvailable = false
			return in
		}(), channelPlan{endState: jobSkipped, endCode: warningBuyerNorwegianBusinessRequired}},
		{"foreign business after the duty: e-mail", all(channelInput{customerType: "business", today: after}),
			channelPlan{channels: []string{channelEhf, channelEmail}}},
		{"business without mail", func() channelInput {
			in := all(channelInput{customerType: "business"})
			in.mailAvailable = false
			return in
		}(), channelPlan{channels: []string{channelEhf}, endState: jobFailed, endCode: codeMailUnavailable}},
		{"business without an invoice e-mail", func() channelInput {
			in := all(channelInput{customerType: "business"})
			in.hasInvoiceEmail = false
			return in
		}(), channelPlan{channels: []string{channelEhf}, endState: jobFailed, endCode: codeNoInvoiceEmail}},
		{"business: never eFaktura", func() channelInput {
			in := all(channelInput{customerType: "business"})
			in.ehfAvailable = false
			return in
		}(), channelPlan{channels: []string{channelEmail}}},
		{"unknown customer with an org number is a business", all(channelInput{norwegianBusiness: true}),
			channelPlan{channels: []string{channelEhf, channelEmail}}},

		// The preference wins.
		{"prefers EHF", all(channelInput{customerType: "person", preference: "ehf"}),
			channelPlan{channels: []string{channelEhf}, preferred: true}},
		{"prefers EHF, EHF off: fails", func() channelInput {
			in := all(channelInput{customerType: "business", preference: "ehf"})
			in.ehfAvailable = false
			return in
		}(), channelPlan{preferred: true, endState: jobFailed, endCode: codeEhfUnavailable}},
		{"prefers eFaktura", all(channelInput{customerType: "person", preference: "efaktura"}),
			channelPlan{channels: []string{channelEfaktura}, preferred: true}},
		{"prefers eFaktura, eFaktura switched off: fails, never picked", func() channelInput {
			in := all(channelInput{customerType: "person", preference: "efaktura"})
			in.efakturaAvailable = false
			return in
		}(), channelPlan{preferred: true, endState: jobFailed, endCode: codeEfakturaUnavailable}},
		{"prefers eFaktura, a credit note: the next channels", all(channelInput{customerType: "person", preference: "efaktura", creditNote: true}),
			channelPlan{channels: []string{channelEmail, channelPrint}}},
		{"prefers e-mail", all(channelInput{customerType: "business", preference: "email"}),
			channelPlan{channels: []string{channelEmail}, preferred: true}},
		{"prefers e-mail, a Norwegian business after the duty: skipped", all(channelInput{customerType: "business", preference: "email", norwegianBusiness: true, today: after}),
			channelPlan{preferred: true, endState: jobSkipped, endCode: warningBuyerNorwegianBusinessRequired}},
		{"prefers e-mail, a Norwegian business before the duty", all(channelInput{customerType: "business", preference: "email", norwegianBusiness: true}),
			channelPlan{channels: []string{channelEmail}, preferred: true}},
		{"prefers e-mail, no mail: fails", func() channelInput {
			in := all(channelInput{customerType: "person", preference: "email"})
			in.mailAvailable = false
			return in
		}(), channelPlan{preferred: true, endState: jobFailed, endCode: codeMailUnavailable}},
		{"prefers paper", all(channelInput{customerType: "business", preference: "paper", norwegianBusiness: true, today: after}),
			channelPlan{channels: []string{channelPrint}, preferred: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chooseSendChannel(tt.in)
			if !slices.Equal(got.channels, tt.want.channels) || got.preferred != tt.want.preferred ||
				got.endState != tt.want.endState || got.endCode != tt.want.endCode {
				t.Errorf("chooseSendChannel = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Only "not by this way" falls through, and only on eFaktura and EHF.
func TestFallsThrough(t *testing.T) {
	for _, tt := range []struct {
		channel, code string
		want          bool
	}{
		{channelEfaktura, codeEfakturaNotFound, true},
		{channelEfaktura, codeEfakturaDueTooSoon, true},
		{channelEfaktura, codeEfakturaDueTooFar, true},
		{channelEfaktura, codeCurrencyNotNOK, true},
		{channelEfaktura, codeKidMissing, true},
		{channelEfaktura, codeCustomerAnonymised, false},
		{channelEhf, codeNoPeppolID, true},
		{channelEhf, codePeppolNotReceivable, true},
		{channelEhf, codeBuyerReferenceMissing, false},
		{channelEmail, codeNoInvoiceEmail, false},
	} {
		if got := fallsThrough(tt.channel, tt.code); got != tt.want {
			t.Errorf("fallsThrough(%s, %s) = %v, want %v", tt.channel, tt.code, got, tt.want)
		}
	}
}
