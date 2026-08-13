using System.Net.Mail;
using System.Security.Claims;

using Vantigo.Configuration;

namespace Vantigo.Identity.Endpoints.Auth;

internal sealed record StaticOidcClaimValidationResult(
    bool Succeeded,
    string? Error,
    string? TenantId = null,
    string? ObjectId = null);

/// <summary>
/// Provider-specific claim policy kept separate from OIDC event wiring so it can
/// be unit tested without an external identity provider.
/// </summary>
internal static class StaticOidcClaimValidation
{
    public static StaticOidcClaimValidationResult Validate(
        ClaimsPrincipal principal,
        WorkforceOidcOptions options) => options.Provider switch
        {
            WorkforceOidcOptions.EntraProvider => ValidateEntra(principal, options),
            WorkforceOidcOptions.GoogleProvider => ValidateGoogle(principal, options),
            _ => new(false, "The configured OIDC provider is not supported."),
        };

    private static StaticOidcClaimValidationResult ValidateEntra(
        ClaimsPrincipal principal,
        WorkforceOidcOptions options)
    {
        var tid = principal.FindFirst("tid")?.Value;
        var oid = principal.FindFirst("oid")?.Value;
        var azp = principal.FindFirst("azp")?.Value;
        if (!Guid.TryParse(tid, out var tenant) ||
            !Guid.TryParse(options.TenantId, out var configuredTenant) ||
            tenant != configuredTenant)
            return new(false, "The Entra tenant claim does not match the configured tenant.");
        if (!Guid.TryParse(oid, out _))
            return new(false, "The Entra object-id claim is invalid.");
        // ASP.NET Core validates the configured audience. When an ID token has
        // multiple audiences, azp identifies the authorized party and must be
        // the configured client, never merely another accepted audience.
        var audiences = principal.FindAll("aud").Select(claim => claim.Value).ToArray();
        if (audiences.Length > 1 && !string.Equals(azp, options.ClientId, StringComparison.Ordinal))
            return new(false, "The Entra authorized-party claim does not match the client.");
        return new(true, null, tenant.ToString("D"), oid);
    }

    private static StaticOidcClaimValidationResult ValidateGoogle(
        ClaimsPrincipal principal,
        WorkforceOidcOptions options)
    {
        var email = principal.FindFirst("email")?.Value;
        var verified = principal.FindFirst("email_verified")?.Value;
        var hostedDomain = principal.FindFirst("hd")?.Value?.Trim().TrimEnd('.').ToLowerInvariant();
        if (!string.Equals(verified, "true", StringComparison.OrdinalIgnoreCase))
            return new(false, "The Google email address is not verified.");
        if (!IsValidEmail(email, out var emailDomain))
            return new(false, "The Google email address is invalid.");
        if (string.IsNullOrEmpty(hostedDomain) || !string.Equals(hostedDomain, emailDomain, StringComparison.Ordinal))
            return new(false, "The Google hosted domain does not match the email domain.");
        if (!options.AllowedDomains.Contains(hostedDomain, StringComparer.Ordinal))
            return new(false, "The Google hosted domain is not allowed.");
        return new(true, null);
    }

    private static bool IsValidEmail(string? value, out string? domain)
    {
        domain = null;
        if (string.IsNullOrWhiteSpace(value) || value.Any(char.IsWhiteSpace) || value.Any(char.IsControl) ||
            value.Count(character => character == '@') != 1 || value.Length > 256)
            return false;
        try
        {
            var parsed = new MailAddress(value);
            if (!string.Equals(parsed.Address, value, StringComparison.Ordinal)) return false;
            var at = value.LastIndexOf('@');
            domain = value[(at + 1)..].TrimEnd('.').ToLowerInvariant();
            return at > 0 && domain.Length > 0 && domain.Contains('.', StringComparison.Ordinal);
        }
        catch (FormatException)
        {
            return false;
        }
    }
}