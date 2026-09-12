package products

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file ports the .NET Products module's field-level value validation
// (Endpoints/Products/Dtos/ProductRequest.cs, ProductPriceRequest.cs,
// Domain/Products/Product.cs, ProductVariant.cs, ProductPrice.cs; products
// inventory §1.3, §2): each a normalize-or-explain function, kept as plain
// functions over primitive types (never gen.* request shapes) so this file
// stays independent of the generated contract code, mirroring the customers
// module's values.go. The exact error message text is the contract — this
// module has no machine-readable error codes at all (inventory §1) — so
// every string below is copied verbatim from the .NET source, never
// paraphrased.

const (
	productNameMaxLength        = 200 // Product.NameMaxLength
	productDescriptionMaxLength = 4000
	variantSkuMaxLength         = 64 // ProductVariant.SkuMaxLength
	variantUnitMaxLength        = 20 // ProductVariant.UnitMaxLength
	variantDefaultUnit          = "pcs"
	currencyLength              = 3 // ProductPrice.CurrencyLength
)

// utf16Length is len(s) as .NET's string.Length counts it: UTF-16 code
// units, not runes. Duplicated from customers' values.go (itself duplicated
// from internal/identity/passwords.go): depguard forbids this module
// importing either, and the function is four lines.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// validateProductName is ProductRequest.Validate's name check
// (ProductRequest.cs:25-32): blank is a required-field error; the length
// check runs against the *trimmed* value, matching `Name.Trim().Length`.
func validateProductName(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "'name' is required."
	}
	trimmed := strings.TrimSpace(raw)
	if n := utf16Length(trimmed); n > productNameMaxLength {
		return "", fmt.Sprintf("'name' must be at most %d characters.", productNameMaxLength)
	}
	return trimmed, ""
}

// validateProductDescriptionLength is ProductRequest.Validate's description
// check (:50-53): a length check only, run against the trimmed raw value,
// and only when the field is present at all — blank/absent is never an
// error here (it becomes "no description" separately, via
// normalizeDescription, the endpoint's own NormalizeOptional).
func validateProductDescriptionLength(raw *string) string {
	if raw == nil {
		return ""
	}
	if n := utf16Length(strings.TrimSpace(*raw)); n > productDescriptionMaxLength {
		return fmt.Sprintf("'description' must be at most %d characters.", productDescriptionMaxLength)
	}
	return ""
}

// normalizeDescription is CreateProductEndpoint/UpdateProductEndpoint's
// NormalizeOptional (CreateProductEndpoint.cs:114-115): blank or
// whitespace-only becomes nil, otherwise the trimmed value.
func normalizeDescription(raw *string) *string {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	v := strings.TrimSpace(*raw)
	return &v
}

// validateProductType is ProductRequest.Validate's type check (:34-37):
// "Goods" or "Service", parsed case-insensitively (Enum.TryParse
// ignoreCase:true) but always canonicalized to Enum.ToString()'s exact
// PascalCase spelling on success — a client sending "goods" gets back
// "Goods", not "goods".
func validateProductType(raw string) (string, string) {
	switch strings.ToLower(raw) {
	case "goods":
		return "Goods", ""
	case "service":
		return "Service", ""
	default:
		return "", fmt.Sprintf("'type' must be one of 'Goods' or 'Service', but was '%s'.", raw)
	}
}

// validateProductStatus is ProductRequest.Validate's status check (:39-43)
// and the identical check GetProductsEndpoint.Validate runs on the query
// parameter: "Draft", "Active" or "Discontinued", case-insensitive in,
// canonical PascalCase out.
func validateProductStatus(raw string) (string, string) {
	switch strings.ToLower(raw) {
	case "draft":
		return "Draft", ""
	case "active":
		return "Active", ""
	case "discontinued":
		return "Discontinued", ""
	default:
		return "", fmt.Sprintf("'status' must be one of 'Draft', 'Active' or 'Discontinued', but was '%s'.", raw)
	}
}

// validateTaxCategoryID is ProductRequest.Validate's taxCategoryId check
// (:45-48).
func validateTaxCategoryID(id int32) string {
	if id < 1 {
		return fmt.Sprintf("'taxCategoryId' must be 1 or greater, but was %d.", id)
	}
	return ""
}

// validateSKU is VariantRequest.Validate's sku check (:90-98).
func validateSKU(raw string) (string, string) {
	sku := strings.TrimSpace(raw)
	if sku == "" {
		return "", "'sku' is required."
	}
	if n := utf16Length(sku); n > variantSkuMaxLength {
		return "", fmt.Sprintf("'sku' must be at most %d characters.", variantSkuMaxLength)
	}
	return sku, ""
}

// normalizeOptionalString is VariantRequest.NormalizeOptional (:171-172):
// blank/whitespace-only becomes nil, otherwise the trimmed value.
func normalizeOptionalString(raw *string) *string {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	v := strings.TrimSpace(*raw)
	return &v
}

// validateBarcode is VariantRequest.Validate's barcode check (:100-105): a
// GTIN-8/12/13/14 when present, absent entirely when blank.
func validateBarcode(raw *string) (*string, string) {
	b := normalizeOptionalString(raw)
	if b == nil {
		return nil, ""
	}
	if !isValidGTIN(*b) {
		return nil, "'barcode' must be a valid GTIN-8, GTIN-12, GTIN-13 or GTIN-14: digits only with a correct check digit."
	}
	return b, ""
}

// validateUnit is VariantRequest.Validate's unit check (:107-111): absent
// (nil) defers entirely to variantDefaultUnit ("pcs"); present-but-blank or
// present-and-too-long is a field error; present, non-blank and short
// enough is trimmed and kept.
func validateUnit(raw *string) (string, string) {
	if raw == nil {
		return variantDefaultUnit, ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" || utf16Length(trimmed) > variantUnitMaxLength {
		return "", fmt.Sprintf("'unit' must be a non-empty value of at most %d characters.", variantUnitMaxLength)
	}
	return trimmed, ""
}

// validateNonNegative is VariantRequest.ValidateNonNegative (:159-169), used
// for standardCost, weightKg, lengthCm, widthCm and heightCm alike: absent
// is fine, present-and-negative is a field error keyed by field.
func validateNonNegative(field string, v *float64) string {
	if v != nil && *v < 0 {
		return fmt.Sprintf("'%s' must be zero or greater.", field)
	}
	return ""
}

// validateCurrency is ProductPriceRequest.Validate's currency check
// (:20-25): exactly three ASCII letters after trimming, not checked against
// a real ISO 4217 list ("ZZZ" passes, products inventory §2), uppercased on
// success (ProductPriceRequest.ToDomain:41).
func validateCurrency(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || utf16Length(trimmed) != currencyLength || !isASCIILetters(trimmed) {
		return "", "'currency' must be a three-letter ISO 4217 currency code."
	}
	return strings.ToUpper(trimmed), ""
}

func isASCIILetters(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// formatAmount is Amount.ToString(CultureInfo.InvariantCulture) as used in
// ProductPriceRequest.Validate's amount error (:29-30): the shortest
// decimal text that round-trips, no thousands separators.
func formatAmount(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// numericFromFloat converts a required decimal wire value (JSON float64,
// contract format: double) to pgtype.Numeric for storage, formatted with
// its shortest round-tripping decimal text rather than rounded in Go:
// products inventory §2 says Postgres's numeric(p,s) column scale is where
// rounding happens, exactly as .NET relied on Postgres to do it.
//
// Scan's error is returned rather than discarded. It can only fire for an
// infinity ("+Inf"/"-Inf"): Postgres's numeric type has a NaN of its own, so
// Scan accepts "NaN" and the infinities are the only unstorable float64s.
// Either way nothing reaches this from a request today, because every value
// arrives through encoding/json, which refuses to decode a NaN or an
// infinity. Discarding the error would store a silent SQL NULL for a number
// the caller actually sent, so the one remaining way to reach it (a future
// non-JSON caller) fails loudly instead.
func numericFromFloat(v float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(v, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("products: %v is not a storable decimal: %w", v, err)
	}
	return n, nil
}

// numericFromFloatPtr is numericFromFloat for an optional field: nil stays
// an invalid (SQL NULL) pgtype.Numeric, which is the column's own NULL and
// never an error.
func numericFromFloatPtr(v *float64) (pgtype.Numeric, error) {
	if v == nil {
		return pgtype.Numeric{}, nil
	}
	return numericFromFloat(*v)
}

// variantNumerics are a variant's five optional decimal columns, converted
// together so a caller checks one error rather than five and its params
// literal stays one readable block.
type variantNumerics struct {
	StandardCost pgtype.Numeric
	WeightKg     pgtype.Numeric
	LengthCm     pgtype.Numeric
	WidthCm      pgtype.Numeric
	HeightCm     pgtype.Numeric
}

// numericsFromVariant converts every optional decimal a validated variant
// carries, failing on the first value Postgres could not store (see
// numericFromFloat: unreachable from a JSON request, loud rather than a
// silent NULL if it ever becomes reachable).
func numericsFromVariant(v parsedVariant) (variantNumerics, error) {
	var out variantNumerics
	for _, f := range []struct {
		dst *pgtype.Numeric
		src *float64
	}{
		{&out.StandardCost, v.StandardCost},
		{&out.WeightKg, v.WeightKg},
		{&out.LengthCm, v.LengthCm},
		{&out.WidthCm, v.WidthCm},
		{&out.HeightCm, v.HeightCm},
	} {
		n, err := numericFromFloatPtr(f.src)
		if err != nil {
			return variantNumerics{}, err
		}
		*f.dst = n
	}
	return out, nil
}

// floatFromNumeric reads a required pgtype.Numeric column back onto the
// wire as float64 (contract format: double).
func floatFromNumeric(n pgtype.Numeric) float64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

// floatPtrFromNumeric is floatFromNumeric for an optional column: a SQL
// NULL becomes nil, never 0.
func floatPtrFromNumeric(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	v := floatFromNumeric(n)
	return &v
}

// camelCase is .NET's JsonNamingPolicy.CamelCase.ConvertName as applied to
// Dictionary<string,string> keys under JsonSerializerDefaults.Web (products
// inventory §7 oddity 5): lowercase the first rune, leave the rest
// untouched — not a snake_case-aware or word-boundary-aware transform, so
// "ShoeSize" becomes "shoeSize", not "shoe_size" or "shoeSIZE".
func camelCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// camelizeOptionValueKeys is the response-side effect of oddity 5: keys are
// stored and internally compared with whatever casing the client sent, but
// every key is camelCased when the variant goes out on the wire.
func camelizeOptionValueKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[camelCase(k)] = v
	}
	return out
}

// stringPtrEqual and int32PtrEqual report whether two optional fields carry
// the same value, nil included, for PutProductsById's "did anything actually
// change" comparison (products.go).
func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func int32PtrEqual(a, b *int32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
