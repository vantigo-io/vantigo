using System.Security.Cryptography;

using Microsoft.AspNetCore.WebUtilities;

namespace Vantigo.Customers.Api.Services;

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
        var template = configuration["Authentication:Invitations:AcceptUrl"]
            ?? "http://localhost:5173/invitations/accept?token={token}";
        return ReplaceRequiredToken(template, rawToken);
    }

    public static string PasswordResetUrl(IConfiguration configuration, string email, string encodedToken)
    {
        var template = configuration["Authentication:PasswordReset:ResetUrl"]
            ?? "http://localhost:5173/password-reset?email={email}&token={token}";
        if (!template.Contains("{email}", StringComparison.Ordinal))
        {
            throw new InvalidOperationException("Authentication:PasswordReset:ResetUrl must contain the {email} placeholder.");
        }

        return ReplaceRequiredToken(template.Replace("{email}", Uri.EscapeDataString(email), StringComparison.Ordinal), encodedToken);
    }

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