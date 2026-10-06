package bankfiletest

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// CamtEntry is one entry (Ntry) of a camt.054 notification. Status is
// BOOK when empty, CreditDebit CRDT when empty; a zero BookedOn writes no
// BookgDt, as a pending entry has none. BankDomain is
// "Domain/Family/SubFamily" and BankProprietary a proprietary code, each
// written when given; with neither, the entry is PMNT/RCDT/VCOM, as every
// bank writes some code. Txs are its TxDtls; an entry
// without them is written with AmountMinor as its own amount.
type CamtEntry struct {
	Status, CreditDebit         string
	Reversal                    bool
	BookedOn                    time.Time
	BankDomain, BankProprietary string
	AddtlInfo                   string
	Txs                         []CamtTx
	AmountMinor                 int64 // the entry's amount when it has no Txs
}

// CamtTx is one transaction (TxDtls) of an entry: KID is written as a
// structured creditor reference of type SCOR, Ustrd as one unstructured
// line; Debtor and DebtorAccount (an IBAN when it begins with letters)
// under RltdPties.
type CamtTx struct {
	AmountMinor                                    int64
	KID, Ustrd, Debtor, DebtorAccount, AcctSvcrRef string
}

// Camt054 is a camt.054 notification in version — "camt.054.001.02" or
// "camt.054.001.08", each in its own paths — of one notification on
// account (an IBAN when it begins with letters, else a BBAN) holding
// entries in the order given: every entry's amount, its Btch/NbOfTxs and
// the notification's TxsSummry computed, every amount in NOK.
func Camt054(version, msgID string, created time.Time, account string, entries ...CamtEntry) []byte {
	v08 := version == "camt.054.001.08"
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	stamp := created.Format("2006-01-02T15:04:05-07:00")

	w(`<?xml version="1.0" encoding="UTF-8"?>`+"\n"+`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:%s">`+"\n", version)
	w("<BkToCstmrDbtCdtNtfctn>\n<GrpHdr><MsgId>%s</MsgId><CreDtTm>%s</CreDtTm></GrpHdr>\n", esc(msgID), stamp)
	w("<Ntfctn>\n<Id>N%s</Id><CreDtTm>%s</CreDtTm>\n", esc(msgID), stamp)
	w("<Acct><Id>%s</Id><Ccy>NOK</Ccy></Acct>\n", accountID(account))

	amounts := make([]int64, len(entries))
	var credits, debits, credited, debited int64
	for i, e := range entries {
		amounts[i] = e.AmountMinor
		if len(e.Txs) > 0 {
			amounts[i] = 0
			for _, tx := range e.Txs {
				amounts[i] += tx.AmountMinor
			}
		}
		if direction(e) == "DBIT" {
			debits++
			debited += amounts[i]
		} else {
			credits++
			credited += amounts[i]
		}
	}
	net, netDirection := credited-debited, "CRDT"
	if net < 0 {
		net, netDirection = -net, "DBIT"
	}
	w("<TxsSummry>\n<TtlNtries><NbOfNtries>%d</NbOfNtries><Sum>%s</Sum>", credits+debits, amount(credited+debited))
	if v08 {
		w("<TtlNetNtry><Amt>%s</Amt><CdtDbtInd>%s</CdtDbtInd></TtlNetNtry>", amount(net), netDirection)
	} else {
		w("<TtlNetNtryAmt>%s</TtlNetNtryAmt><CdtDbtInd>%s</CdtDbtInd>", amount(net), netDirection)
	}
	w("</TtlNtries>\n<TtlCdtNtries><NbOfNtries>%d</NbOfNtries><Sum>%s</Sum></TtlCdtNtries>\n", credits, amount(credited))
	w("<TtlDbtNtries><NbOfNtries>%d</NbOfNtries><Sum>%s</Sum></TtlDbtNtries>\n</TxsSummry>\n", debits, amount(debited))

	for i, e := range entries {
		status := e.Status
		if status == "" {
			status = "BOOK"
		}
		w("<Ntry>\n<NtryRef>%d</NtryRef><Amt Ccy=\"NOK\">%s</Amt><CdtDbtInd>%s</CdtDbtInd>", i+1, amount(amounts[i]), direction(e))
		if e.Reversal {
			w("<RvslInd>true</RvslInd>")
		}
		if v08 {
			w("<Sts><Cd>%s</Cd></Sts>", esc(status))
		} else {
			w("<Sts>%s</Sts>", esc(status))
		}
		if !e.BookedOn.IsZero() {
			w("<BookgDt><Dt>%s</Dt></BookgDt><ValDt><Dt>%[1]s</Dt></ValDt>", e.BookedOn.Format("2006-01-02"))
		}
		domain := e.BankDomain
		if domain == "" && e.BankProprietary == "" {
			domain = "PMNT/RCDT/VCOM"
		}
		w("<BkTxCd>")
		if parts := strings.SplitN(domain, "/", 3); len(parts) == 3 {
			w("<Domn><Cd>%s</Cd><Fmly><Cd>%s</Cd><SubFmlyCd>%s</SubFmlyCd></Fmly></Domn>", esc(parts[0]), esc(parts[1]), esc(parts[2]))
		}
		if e.BankProprietary != "" {
			w("<Prtry><Cd>%s</Cd><Issr>NETS</Issr></Prtry>", esc(e.BankProprietary))
		}
		w("</BkTxCd>")
		w("\n")
		if len(e.Txs) > 0 {
			w("<NtryDtls>\n<Btch><NbOfTxs>%d</NbOfTxs></Btch>\n", len(e.Txs))
			for _, tx := range e.Txs {
				writeTx(&b, v08, direction(e), tx)
			}
			w("</NtryDtls>\n")
		}
		if e.AddtlInfo != "" {
			w("<AddtlNtryInf>%s</AddtlNtryInf>\n", esc(e.AddtlInfo))
		}
		w("</Ntry>\n")
	}
	w("</Ntfctn>\n</BkToCstmrDbtCdtNtfctn>\n</Document>\n")
	return []byte(b.String())
}

// writeTx writes one TxDtls in its version's paths: .02's amount under
// AmtDtls/TxAmt and debtor name under Dbtr/Nm, .08's amount and direction
// on the TxDtls itself and debtor name under Dbtr/Pty/Nm.
func writeTx(b *strings.Builder, v08 bool, dir string, tx CamtTx) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("<TxDtls>")
	if tx.AcctSvcrRef != "" {
		w("<Refs><AcctSvcrRef>%s</AcctSvcrRef></Refs>", esc(tx.AcctSvcrRef))
	}
	if v08 {
		w("<Amt Ccy=\"NOK\">%s</Amt><CdtDbtInd>%s</CdtDbtInd>", amount(tx.AmountMinor), dir)
	} else {
		w("<AmtDtls><TxAmt><Amt Ccy=\"NOK\">%s</Amt></TxAmt></AmtDtls>", amount(tx.AmountMinor))
	}
	if tx.Debtor != "" || tx.DebtorAccount != "" {
		w("<RltdPties>")
		if tx.Debtor != "" && v08 {
			w("<Dbtr><Pty><Nm>%s</Nm></Pty></Dbtr>", esc(tx.Debtor))
		} else if tx.Debtor != "" {
			w("<Dbtr><Nm>%s</Nm></Dbtr>", esc(tx.Debtor))
		}
		if tx.DebtorAccount != "" {
			w("<DbtrAcct><Id>%s</Id></DbtrAcct>", accountID(tx.DebtorAccount))
		}
		w("</RltdPties>")
	}
	if tx.KID != "" || tx.Ustrd != "" {
		w("<RmtInf>")
		if tx.Ustrd != "" {
			w("<Ustrd>%s</Ustrd>", esc(tx.Ustrd))
		}
		if tx.KID != "" {
			w("<Strd><CdtrRefInf><Tp><CdOrPrtry><Cd>SCOR</Cd></CdOrPrtry></Tp><Ref>%s</Ref></CdtrRefInf></Strd>", esc(tx.KID))
		}
		w("</RmtInf>")
	}
	w("</TxDtls>\n")
}

func direction(e CamtEntry) string {
	if e.CreditDebit == "" {
		return "CRDT"
	}
	return e.CreditDebit
}

// accountID is an account's Id element's content: an IBAN when s begins
// with letters, else Othr/Id with the BBAN scheme.
func accountID(s string) string {
	if s != "" && s[0] >= 'A' && s[0] <= 'Z' {
		return "<IBAN>" + esc(s) + "</IBAN>"
	}
	return "<Othr><Id>" + esc(s) + "</Id><SchmeNm><Cd>BBAN</Cd></SchmeNm></Othr>"
}

// amount is øre as a camt amount: kroner with two decimals.
func amount(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
