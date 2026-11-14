package mva

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"time"
)

// Namespace is the return's: skattemeldingformerverdiavgift v1.0.
const Namespace = "no:skatteetaten:fastsetting:avgift:mva:skattemeldingformerverdiavgift:v1.0"

// Period is a term as the schema names it.
type Period struct {
	Year int
	// Kind is bimonthly or yearly.
	Kind string
	// Number is the term in its year: 1 to 6, or 1.
	Number int
}

// twoMonths is the schema's SkattleggingsperiodeToMaaneder, by term.
var twoMonths = []string{"januar-februar", "mars-april", "mai-juni", "juli-august", "september-oktober", "november-desember"}

// Yearly is the schema's SkattleggingsperiodeAar.
const Yearly = "aarlig"

// Name is the term as the schema writes it: januar-februar … or aarlig.
func (p Period) Name() string {
	if p.Kind == "yearly" {
		return Yearly
	}
	if p.Number >= 1 && p.Number <= len(twoMonths) {
		return twoMonths[p.Number-1]
	}
	return ""
}

// Submission is what the XML says about who sends it and from where.
type Submission struct {
	// Reference is regnskapssystemsreferanse, unique per submission.
	Reference string
	// SystemName and SystemVersion are the accounting system's.
	SystemName, SystemVersion string
	// OrganisationNumber is the seller's nine digits; "" leaves it out.
	OrganisationNumber string
	Period             Period
}

// organisationNumber is the schema's Organisasjonsnummer.
var organisationNumber = regexp.MustCompile(`^[0-9]{9}$`)

// ValidOrganisationNumber reports whether n is nine digits, as the schema
// takes it.
func ValidOrganisationNumber(n string) bool { return organisationNumber.MatchString(n) }

type document struct {
	XMLName                       xml.Name      `xml:"mvaMeldingDto"`
	Xmlns                         string        `xml:"xmlns,attr"`
	Innsending                    innsending    `xml:"innsending"`
	SkattegrunnlagOgBeregnetSkatt grunnlag      `xml:"skattegrunnlagOgBeregnetSkatt"`
	Betalingsinformasjon          struct{}      `xml:"betalingsinformasjon"`
	Skattepliktig                 skattepliktig `xml:"skattepliktig"`
	Meldingskategori              string        `xml:"meldingskategori"`
}

type innsending struct {
	Regnskapssystemsreferanse string          `xml:"regnskapssystemsreferanse"`
	Regnskapssystem           regnskapssystem `xml:"regnskapssystem"`
}

type regnskapssystem struct {
	Systemnavn    string `xml:"systemnavn"`
	Systemversjon string `xml:"systemversjon,omitempty"`
}

type grunnlag struct {
	Skattleggingsperiode   skattleggingsperiode `xml:"skattleggingsperiode"`
	FastsattMerverdiavgift string               `xml:"fastsattMerverdiavgift"`
	Linjer                 []linje              `xml:"mvaSpesifikasjonslinje"`
}

type skattleggingsperiode struct {
	Periode periode `xml:"periode"`
	Aar     int     `xml:"aar"`
}

type periode struct {
	ToMaaneder string `xml:"skattleggingsperiodeToMaaneder,omitempty"`
	Aar        string `xml:"skattleggingsperiodeAar,omitempty"`
}

type linje struct {
	MvaKode               string   `xml:"mvaKode"`
	Spesifikasjon         string   `xml:"spesifikasjon,omitempty"`
	MvaKodeRegnskapsystem string   `xml:"mvaKodeRegnskapsystem,omitempty"`
	Grunnlag              string   `xml:"grunnlag,omitempty"`
	Sats                  string   `xml:"sats,omitempty"`
	Merverdiavgift        string   `xml:"merverdiavgift"`
	Merknad               *merknad `xml:"merknad"`
}

type merknad struct {
	UtvalgtMerknad string `xml:"utvalgtMerknad"`
}

type skattepliktig struct {
	Organisasjonsnummer string `xml:"organisasjonsnummer,omitempty"`
}

// maxText is the schema's Tekst: at most 4000 characters.
const maxText = 4000

// XML is the return in the skattemeldingformerverdiavgift v1.0 schema, the
// meldingskategori alminnelig, indented, with its XML declaration.
func XML(r Return, s Submission) ([]byte, error) {
	name := s.Period.Name()
	if name == "" {
		return nil, fmt.Errorf("mva: no such term %+v", s.Period)
	}
	if s.OrganisationNumber != "" && !ValidOrganisationNumber(s.OrganisationNumber) {
		return nil, fmt.Errorf("mva: organisation number %q is not nine digits", s.OrganisationNumber)
	}
	d := document{Xmlns: Namespace, Meldingskategori: "alminnelig",
		Innsending: innsending{Regnskapssystemsreferanse: s.Reference,
			Regnskapssystem: regnskapssystem{Systemnavn: s.SystemName, Systemversjon: s.SystemVersion}},
		Skattepliktig: skattepliktig{Organisasjonsnummer: s.OrganisationNumber}}
	g := &d.SkattegrunnlagOgBeregnetSkatt
	g.Skattleggingsperiode.Aar = s.Period.Year
	if s.Period.Kind == "yearly" {
		g.Skattleggingsperiode.Periode.Aar = name
	} else {
		g.Skattleggingsperiode.Periode.ToMaaneder = name
	}
	g.FastsattMerverdiavgift = r.Total.String()
	for _, l := range r.Lines {
		x := linje{MvaKode: l.Code, Spesifikasjon: l.Specification, MvaKodeRegnskapsystem: truncate(l.Name), Merverdiavgift: l.VAT.String()}
		if l.Side == SideOutput && l.Specification == "" {
			x.Grunnlag, x.Sats = l.Base.String(), l.Rate
		}
		if l.Remark != "" {
			x.Merknad = &merknad{UtvalgtMerknad: l.Remark}
		}
		g.Linjer = append(g.Linjer, x)
	}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	enc := xml.NewEncoder(&b)
	enc.Indent("", "    ")
	if err := enc.Encode(d); err != nil {
		return nil, fmt.Errorf("mva: write the return: %w", err)
	}
	b.WriteString("\n")
	return b.Bytes(), nil
}

// truncate keeps a text within the schema's Tekst.
func truncate(s string) string {
	r := []rune(s)
	if len(r) > maxText {
		return string(r[:maxText])
	}
	return s
}

// DueDate is the day a term's return and payment are due (the research's
// §2.3, Skatteetaten's deadlines): one month and ten days after the term
// ends — the 10th of the second month after it — except the third two-month
// term, due 31 August; a yearly term on 10 March of the next year.
func DueDate(p Period) time.Time {
	if p.Kind == "yearly" {
		return time.Date(p.Year+1, time.March, 10, 0, 0, 0, 0, time.UTC)
	}
	if p.Number == 3 {
		return time.Date(p.Year, time.August, 31, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(p.Year, time.Month(2*p.Number+2), 10, 0, 0, 0, 0, time.UTC)
}
