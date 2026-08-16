using System.Security.Cryptography;
using System.Text;

namespace Vantigo.Communications.Services;

internal static class MailgunSignatureVerifier
{
    internal static bool Verify(string? timestamp, string? token, string? signature, string signingKey,
        DateTimeOffset now, TimeSpan pastSkew, TimeSpan futureSkew)
    {
        if (string.IsNullOrWhiteSpace(timestamp) || timestamp.Length > 32 ||
            string.IsNullOrWhiteSpace(token) || token.Length > 256 || token.Any(char.IsControl) ||
            string.IsNullOrWhiteSpace(signature) || signature.Length != 64 || string.IsNullOrWhiteSpace(signingKey))
            return false;
        if (!long.TryParse(timestamp, System.Globalization.NumberStyles.None, System.Globalization.CultureInfo.InvariantCulture, out var seconds))
            return false;

        DateTimeOffset issuedAt;
        try { issuedAt = DateTimeOffset.FromUnixTimeSeconds(seconds); }
        catch (ArgumentOutOfRangeException) { return false; }
        if (issuedAt < now.Subtract(pastSkew) || issuedAt > now.Add(futureSkew)) return false;

        byte[] supplied;
        try { supplied = Convert.FromHexString(signature); }
        catch (FormatException) { return false; }
        using var hmac = new HMACSHA256(Encoding.UTF8.GetBytes(signingKey));
        var expected = hmac.ComputeHash(Encoding.ASCII.GetBytes(timestamp + token));
        return CryptographicOperations.FixedTimeEquals(expected, supplied);
    }
}