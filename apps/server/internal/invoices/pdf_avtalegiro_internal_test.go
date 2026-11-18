package invoices

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The AvtaleGiro line (invoices phase 6 reading 11): a person's invoice with
// the customer KID frozen on its snapshot prints it under the payment
// details, in the document's language, pinned by a render hash of its own;
// without one the render is the pinned one, byte for byte.
func TestPDFModel_TheAvtaleGiroLine(t *testing.T) {
	t.Parallel()
	d := anInvoice()
	d.kid = "0010009"
	d.avtaleGiroKid = "9000012344"
	if m := buildPDFModel(d); m.avtaleGiroNote != "AvtaleGiro: opprett avtale i nettbanken med KID 9000012344" {
		t.Errorf("nb = %q", m.avtaleGiroNote)
	}
	d.language = "en"
	if m := buildPDFModel(d); m.avtaleGiroNote != "AvtaleGiro: set up an agreement in your online bank with KID 9000012344" {
		t.Errorf("en = %q", m.avtaleGiroNote)
	}
	if m := buildPDFModel(anInvoice()); m.avtaleGiroNote != "" {
		t.Errorf("without a customer KID = %q", m.avtaleGiroNote)
	}
	d = anInvoice()
	d.avtaleGiroKid = "9000012344"
	body, err := renderPDF(buildPDFModel(d))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != pinnedAvtaleGiroRenderSHA256 {
		t.Errorf("the AvtaleGiro render's SHA-256 = %s, want %s", got, pinnedAvtaleGiroRenderSHA256)
	}
}

const pinnedAvtaleGiroRenderSHA256 = "c6d179ccf1300ddb4543385391c5429a51bdf1ec0d4f28116729bf31cf78d2a8"
