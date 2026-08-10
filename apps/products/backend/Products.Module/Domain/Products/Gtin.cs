namespace Vantigo.Products.Domain.Products;

/// <summary>
/// Validation for GTIN barcodes (GTIN-8, GTIN-12/UPC-A, GTIN-13/EAN-13 and GTIN-14).
/// A GTIN is digits-only with a trailing check digit computed with alternating
/// 3-1 weights from the rightmost digit.
/// </summary>
public static class Gtin
{
    public const int MaxLength = 14;

    /// <summary>
    /// Returns whether the value is a structurally valid GTIN: digits only, a length of
    /// 8, 12, 13 or 14, and a correct check digit.
    /// </summary>
    public static bool IsValid(string value)
    {
        if (value.Length is not (8 or 12 or 13 or 14))
        {
            return false;
        }

        var sum = 0;
        for (var index = 0; index < value.Length; index++)
        {
            var character = value[index];
            if (character is < '0' or > '9')
            {
                return false;
            }

            // Weights alternate 3, 1, 3, ... counted from the digit immediately left
            // of the check digit, which is equivalent to weighting positions whose
            // distance from the right end is odd with 3.
            var weight = (value.Length - 1 - index) % 2 == 1 ? 3 : 1;
            sum += (character - '0') * weight;
        }

        return sum % 10 == 0;
    }
}