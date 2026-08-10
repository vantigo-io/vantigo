using System.Security.Cryptography;

using Microsoft.AspNetCore.WebUtilities;

using Vantigo.Hosting;

namespace Vantigo.Identity.Services;

public static class InvitationTokenService
{
    public static TimeSpan GetLifetime(IConfiguration configuration)
    {
        var configured = configuration.GetValue<TimeSpan?>("Authentication:Invitations:Lifetime");
        if (configured.HasValue && configured.Value >= TimeSpan.FromDays(1) && configured.Value <= TimeSpan.FromDays(30))
        {
            return configured.Value;
        }

        return TimeSpan.FromDays(7);
    }

    public static (string RawToken, string Hash) Create()
    {
        var bytes = RandomNumberGenerator.GetBytes(32);
        var raw = WebEncoders.Base64UrlEncode(bytes);
        return (raw, Convert.ToHexString(SHA256.HashData(bytes)));
    }

    public static string Hash(string rawToken)
    {
        if (string.IsNullOrWhiteSpace(rawToken))
        {
            return string.Empty;
        }

        try
        {
            return Convert.ToHexString(SHA256.HashData(WebEncoders.Base64UrlDecode(rawToken)));
        }
        catch (FormatException)
        {
            return string.Empty;
        }
    }

    public static string InvitationUrl(IConfiguration configuration, string rawToken)
    {
        // Resolution order: explicit template > derived from App:PublicOrigin
        // and App:BasePath > development fallback.
        var template = configuration["Authentication:Invitations:AcceptUrl"]
            ?? DerivedUrl(configuration, "/invitations/accept?token={token}")
            ?? "http://localhost:5173/invitations/accept?token={token}";
        return ReplaceRequiredToken(template, rawToken);
    }

    public static string PasswordResetUrl(IConfiguration configuration, string email, string encodedToken)
    {
        var template = configuration["Authentication:PasswordReset:ResetUrl"]
            ?? DerivedUrl(configuration, "/password-reset?email={email}&token={token}")
            ?? "http://localhost:5173/password-reset?email={email}&token={token}";
        if (!template.Contains("{email}", StringComparison.Ordinal))
        {
            throw new InvalidOperationException("Authentication:PasswordReset:ResetUrl must contain the {email} placeholder.");
        }

        return ReplaceRequiredToken(template.Replace("{email}", Uri.EscapeDataString(email), StringComparison.Ordinal), encodedToken);
    }

    private static string? DerivedUrl(IConfiguration configuration, string appRelativePathAndQuery) =>
        new AppPublicUrls(configuration).PublicUrl(appRelativePathAndQuery);

    private static string ReplaceRequiredToken(string template, string rawToken)
    {
        if (!template.Contains("{token}", StringComparison.Ordinal))
        {
            throw new InvalidOperationException("The configured security-mail URL must contain the {token} placeholder.");
        }

        return template
            .Replace("{token}", Uri.EscapeDataString(rawToken), StringComparison.Ordinal);
    }
}