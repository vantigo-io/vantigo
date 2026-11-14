package mva_test

import (
	"encoding/xml"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/mva"
)

// XSD is the return's schema, as Skatteetaten publishes it.
var XSD = filepath.Join("testdata", "xsd", "no.skatteetaten.fastsetting.avgift.mva.skattemeldingformerverdiavgift.v1.0.xsd")

// validate holds file to the XSD with xmllint, when it is on the PATH; CI
// validates every golden with tools/mva/validate.sh (Java) whatever this
// finds.
func validate(t *testing.T, file string) {
	t.Helper()
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Logf("xmllint is not on the PATH: %s is validated by tools/mva/validate.sh", file)
		return
	}
	out, err := exec.Command("xmllint", "--noout", "--schema", XSD, file).CombinedOutput()
	if err != nil {
		t.Errorf("%s does not validate: %v\n%s", file, err, out)
	}
}

// Skatteetaten's own examples validate, so the schema and the validator are
// what this package's goldens are held to.
func TestTheExamplesValidate(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "examples", "*.xml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples: %v", err)
	}
	for _, f := range files {
		validate(t, f)
	}
}

// The groups and the order are the mvaKodeSAFT list's, code by code: every
// code the list marks for the general return is in the table with its tema's
// order and its own.
func TestTheCodesAreSkatteetatensList(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "kodelister", "mvaKodeSAFT.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Codes []struct {
			Name  string `xml:"tekniskNavn"`
			Extra struct {
				General    string `xml:"alminneligPrimær"`
				GroupOrder string `xml:"temaRekkefoelge"`
				Order      string `xml:"kodeRekkefoelgeITema"`
			} `xml:"kodetillegg"`
		} `xml:"kode"`
	}
	if err := xml.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, c := range list.Codes {
		if c.Extra.General != "SANN" {
			continue
		}
		seen++
		group, order := mva.Order(c.Name)
		if strconv.Itoa(group) != c.Extra.GroupOrder || strconv.Itoa(order) != c.Extra.Order {
			t.Errorf("code %s: order %d.%d, the list says %s.%s", c.Name, group, order, c.Extra.GroupOrder, c.Extra.Order)
		}
	}
	if seen != 25 {
		t.Errorf("%d codes for the general return, want 25", seen)
	}
	for _, c := range []string{"0", "7", "20", "21", "22"} {
		if mva.GroupOf(c) != mva.NotReported {
			t.Errorf("code %s is in the return", c)
		}
	}
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic(s)
	}
	return r
}

// line is a return line as the examples write it.
type line struct {
	Code   string `xml:"mvaKode"`
	Spec   string `xml:"spesifikasjon"`
	Base   string `xml:"grunnlag"`
	Rate   string `xml:"sats"`
	VAT    string `xml:"merverdiavgift"`
	Remark string `xml:"merknad>utvalgtMerknad"`
}

func linesOf(t *testing.T, raw []byte) (string, []line) {
	t.Helper()
	var doc struct {
		Total string `xml:"skattegrunnlagOgBeregnetSkatt>fastsattMerverdiavgift"`
		Lines []line `xml:"skattegrunnlagOgBeregnetSkatt>mvaSpesifikasjonslinje"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Total, doc.Lines
}

// The return's shape is the published examples': reverse charge as two lines
// of one code with opposite signs, the base and rate on the output side
// only, and a loss's VAT as tapPåKrav on the input code.
func TestTheShapeIsTheExamples(t *testing.T) {
	for _, tc := range []struct {
		example string
		figures []mva.Figure
	}{
		{"mvakode81.xml", []mva.Figure{{Code: "81", Direction: "input", ReverseCharge: true, RatePercent: "25.00",
			Base: rat("10000"), Calculated: rat("2500"), Deducted: rat("-2500")}}},
		{"mvakode86.xml", []mva.Figure{{Code: "86", Direction: "input", ReverseCharge: true, RatePercent: "25.00",
			Base: rat("10000.40"), Calculated: rat("2500.10"), Deducted: rat("-2500.10")}}},
		// The ledger posts a loss with the output code it was charged with.
		{"tappaakrav.xml", []mva.Figure{{Code: "3", Direction: "output", RatePercent: "25.00", Specification: mva.TapPaaKrav,
			Loss: rat("-100000")}}},
	} {
		t.Run(tc.example, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "examples", tc.example))
			if err != nil {
				t.Fatal(err)
			}
			wantTotal, want := linesOf(t, raw)
			out, err := mva.XML(mva.Build(tc.figures), mva.Submission{Reference: "1", SystemName: "Vantigo",
				OrganisationNumber: "913238254", Period: mva.Period{Year: 2021, Kind: "bimonthly", Number: 2}})
			if err != nil {
				t.Fatal(err)
			}
			gotTotal, got := linesOf(t, out)
			if gotTotal != wantTotal || len(got) != len(want) {
				t.Fatalf("total %s, lines %+v; the example: %s, %+v", gotTotal, got, wantTotal, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("line %d = %+v, the example's %+v", i, got[i], want[i])
				}
			}
			file := filepath.Join(t.TempDir(), tc.example)
			if err := os.WriteFile(file, out, 0o600); err != nil {
				t.Fatal(err)
			}
			validate(t, file)
		})
	}
}

// Whole kroner, the øre cut toward zero on each line (R082, R019), the total
// the sum of the lines (R018) and the difference to the ledger kept.
func TestTheOreAreCut(t *testing.T) {
	r := mva.Build([]mva.Figure{
		{Code: "3", Direction: "output", RatePercent: "25.00", Base: rat("1000.99"), Output: rat("250.25")},
		{Code: "1", Direction: "input", RatePercent: "25.00", Deducted: rat("-99.99")},
		{Code: "33", Direction: "output", RatePercent: "12.00", Base: rat("-100.50"), Output: rat("-12.06")},
	})
	got := []string{}
	for _, l := range r.Lines {
		b := "-"
		if l.Base != nil {
			b = l.Base.String()
		}
		got = append(got, l.Code+":"+b+":"+l.Rate+":"+l.VAT.String()+":"+l.Remark)
	}
	want := []string{"3:1000:25:250:", "33:-100:12:-12:kreditnota", "1:-::-99:"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("lines = %v, want %v", got, want)
	}
	if r.Total.String() != "139" || r.ExactTotal.FloatString(2) != "138.20" || r.RoundingDifference().FloatString(2) != "-0.80" {
		t.Errorf("total %s, exact %s, difference %s", r.Total, r.ExactTotal.FloatString(2), r.RoundingDifference().FloatString(2))
	}
}

// The terms' due dates: one month and ten days after, the third term on 31
// August, the sixth in February of the next year, a yearly term on 10 March.
func TestDueDates(t *testing.T) {
	want := []string{"2026-04-10", "2026-06-10", "2026-08-31", "2026-10-10", "2026-12-10", "2027-02-10"}
	for i, w := range want {
		if got := mva.DueDate(mva.Period{Year: 2026, Kind: "bimonthly", Number: i + 1}).Format(time.DateOnly); got != w {
			t.Errorf("term %d due %s, want %s", i+1, got, w)
		}
	}
	if got := mva.DueDate(mva.Period{Year: 2026, Kind: "yearly", Number: 1}).Format(time.DateOnly); got != "2027-03-10" {
		t.Errorf("yearly due %s", got)
	}
}

// An empty term is a return of nothing to pay, and valid.
func TestAnEmptyReturnValidates(t *testing.T) {
	out, err := mva.XML(mva.Build(nil), mva.Submission{Reference: "x", SystemName: "Vantigo", Period: mva.Period{Year: 2026, Kind: "yearly", Number: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`<fastsattMerverdiavgift>0</fastsattMerverdiavgift>`).Match(out) ||
		!strings.Contains(string(out), "<skattleggingsperiodeAar>aarlig</skattleggingsperiodeAar>") {
		t.Errorf("empty return:\n%s", out)
	}
	file := filepath.Join(t.TempDir(), "empty.xml")
	if err := os.WriteFile(file, out, 0o600); err != nil {
		t.Fatal(err)
	}
	validate(t, file)
}

// One line per combination of code, specification and mvaKodeRegnskapsystem
// (Skatteetaten's model, L1): two figures of one input code — a rate that
// changed within the term — make one deduction line, cut once; a reverse
// charge's deduction carries a name of its own, and a loss's line its input
// code's name (L2).
func TestOneLinePerCombination(t *testing.T) {
	r := mva.Build([]mva.Figure{
		{Code: "1", Name: "Input 25 %", Direction: "input", RatePercent: "25.00", Deducted: rat("-10.60")},
		{Code: "1", Name: "Input 25 %", Direction: "input", RatePercent: "24.00", Deducted: rat("-10.60")},
		{Code: "86", Name: "Services", DeductionName: "Services (deduction)", Direction: "input", ReverseCharge: true,
			RatePercent: "25.00", Base: rat("100"), Calculated: rat("25"), Deducted: rat("-25")},
		{Code: "3", Name: "Output 25 %", LossName: "Input 25 %", Direction: "output", RatePercent: "25.00",
			Specification: mva.TapPaaKrav, Loss: rat("10.00")},
	})
	got := []string{}
	for _, l := range r.Lines {
		got = append(got, l.Code+"/"+l.Specification+"/"+l.Name+"/"+l.VAT.String()+"/"+l.Remark)
	}
	want := []string{"1//Input 25 %/-21/", "1/tapPåKrav/Input 25 %/10/", "86//Services/25/", "86//Services (deduction)/-25/"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("lines = %v, want %v", got, want)
	}
}
