package bankfile

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// NormaliseText is a remittance text as the fingerprint compares it:
// trimmed, case-folded, every run of white space one space.
func NormaliseText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// Fingerprints fills every transaction's Fingerprint and Ordinal, in file
// order (D3, R4 §3.6): SHA-256 over the receiving account, the booking
// date, the amount in øre signed (negative for a negative or a debit line),
// the KID — or, without one, the normalised remittance text — the debtor
// account, the archive reference, and the ordinal: the n-th line of txs
// whose other fields are all the same. The same file, or one overlapping
// it, reproduces the ordinals; two identical payments in one file stay two.
//
// Each field is written with its length, so no two lines' fields can run
// together into the same input. The stored fingerprints are compared across
// imports for good: this is never changed.
func Fingerprints(txs []Transaction) {
	seen := map[string]int{}
	for i := range txs {
		tx := &txs[i]
		amount := tx.AmountMinor
		if tx.Negative || tx.Debit {
			amount = -amount
		}
		reference := "kid:" + tx.KID
		if tx.KID == "" {
			reference = "text:" + NormaliseText(tx.RemittanceText)
		}
		var key strings.Builder
		for _, field := range []string{
			tx.Account, tx.BookedOn.Format("2006-01-02"), strconv.FormatInt(amount, 10),
			reference, tx.DebtorAccount, tx.ArchiveRef,
		} {
			key.WriteString(strconv.Itoa(len(field)))
			key.WriteByte(':')
			key.WriteString(field)
			key.WriteByte(';')
		}
		seen[key.String()]++
		tx.Ordinal = seen[key.String()]
		sum := sha256.Sum256([]byte(key.String() + strconv.Itoa(tx.Ordinal)))
		tx.Fingerprint = hex.EncodeToString(sum[:])
	}
}
