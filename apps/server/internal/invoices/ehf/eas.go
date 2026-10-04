package ehf

import "slices"

// easSchemes is the Electronic Address Scheme code list a Peppol endpoint's
// schemeID must be from (PEPPOL-EN16931-CL008), vendored whole so the
// pre-check refuses what the network would (EHF and KID design D11).
//
// Version: OpenPEPPOL/peppol-bis-invoice-3 tag v3.0.20 (commit 261c4584),
// rules/sch/PEPPOL-EN16931-UBL.sch's $eaid, identical to
// structure/codelist/eas.xml at the same tag — 94 codes. Bumped with the
// oracle's artefact pin (tools/ehf/artefacts.lock).
var easSchemes = []string{
	"0002", "0007", "0009", "0037", "0060", "0088", "0096", "0097", "0106", "0130",
	"0135", "0142", "0147", "0151", "0154", "0158", "0170", "0177", "0183", "0184",
	"0188", "0190", "0191", "0192", "0193", "0194", "0195", "0196", "0198", "0199",
	"0200", "0201", "0202", "0203", "0204", "0205", "0208", "0209", "0210", "0211",
	"0212", "0213", "0215", "0216", "0217", "0218", "0221", "0225", "0230", "0235",
	"0240", "0244", "0245", "9910", "9913", "9914", "9915", "9918", "9919", "9920",
	"9922", "9923", "9924", "9925", "9926", "9927", "9928", "9929", "9930", "9931",
	"9932", "9933", "9934", "9935", "9936", "9937", "9938", "9939", "9940", "9941",
	"9942", "9943", "9944", "9945", "9946", "9947", "9948", "9949", "9950", "9951",
	"9952", "9953", "9957", "9959",
}

// EASScheme reports whether scheme is on the Peppol EAS code list, compared
// exactly as the Schematron compares it.
func EASScheme(scheme string) bool {
	return slices.Contains(easSchemes, scheme)
}
