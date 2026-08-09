using System.Security.Cryptography;
using System.Text;

using Microsoft.AspNetCore.Hosting;

namespace Vantigo.Products.Api.Endpoints.Auth;

/// <summary>
/// The one optional workforce OpenID Connect provider supported by this application.
/// It is deliberately configuration-only: no provider settings are persisted in the
/// accounts database and no external provider claims are copied to local accounts.
/// </summary>
internal sealed record WorkforceOidcOptions(
    bool Enabled,
    string Authority,
    string ClientId,
    string ClientSecret,
    string DisplayName,
    string CallbackPath)
{
    internal const string Scheme = "VantigoWorkforceOidc";
    // The OIDC handler's default claim actions may remove protocol claims such as
    // iss. This private claim is populated only after the handler validates the
    // token and is the sole issuer value consumed by completion.
    internal const string ValidatedIssuerClaim = "vantigo:oidc:validated-issuer";
    internal const string CompletionPath = "/auth/oidc/complete";
    internal const string DefaultCallbackPath = "/auth/oidc/callback";
    private const string OpaqueEmailPrefix = "oidc-";
    private const string OpaqueEmailDomain = "sso.invalid";

    private static readonly string[] ReservedCallbackPaths =
    [
        "/auth/providers",
        "/auth/oidc/challenge",
        CompletionPath,
    ];

    internal static WorkforceOidcOptions Disabled { get; } = new(
        false,
        string.Empty,
        string.Empty,
        string.Empty,
        "Workforce SSO",
        DefaultCallbackPath);

    internal static WorkforceOidcOptions Load(IConfiguration configuration, IHostEnvironment environment)
    {
        var section = configuration.GetSection("Authentication:Oidc");
        var authority = section["Authority"]?.Trim() ?? string.Empty;
        var clientId = section["ClientId"]?.Trim() ?? string.Empty;
        var clientSecret = section["ClientSecret"]?.Trim() ?? string.Empty;
        var displayName = section["DisplayName"]?.Trim() ?? string.Empty;
        var callbackPath = section["CallbackPath"]?.Trim() ?? string.Empty;

        // If every required value is absent, the optional display/callback values
        // cannot enable a provider on their own and OIDC remains disabled. Once any
        // required value is supplied, all required values and optional values are
        // validated strictly; partial configuration must never weaken authentication.
        if (string.IsNullOrWhiteSpace(authority) &&
            string.IsNullOrWhiteSpace(clientId) &&
            string.IsNullOrWhiteSpace(clientSecret))
        {
            return Disabled;
        }

        var errors = new List<string>();
        if (string.IsNullOrWhiteSpace(authority))
        {
            errors.Add("Authority is required");
        }
        else if (!TryValidateAuthority(authority, environment, out var authorityError))
        {
            errors.Add(authorityError!);
        }

        if (string.IsNullOrWhiteSpace(clientId))
        {
            errors.Add("ClientId is required");
        }

        if (string.IsNullOrWhiteSpace(clientSecret))
        {
            errors.Add("ClientSecret is required");
        }

        if (displayName.Length > 100 || displayName.Any(char.IsControl))
        {
            errors.Add("DisplayName must be at most 100 characters and contain no control characters");
        }

        displayName = string.IsNullOrWhiteSpace(displayName) ? "Workforce SSO" : displayName;
        callbackPath = string.IsNullOrWhiteSpace(callbackPath) ? DefaultCallbackPath : callbackPath;
        if (ReservedCallbackPaths.Contains(callbackPath, StringComparer.OrdinalIgnoreCase))
        {
            errors.Add($"CallbackPath cannot collide with a local authentication route ({callbackPath})");
        }
        else if (!IsSafeCallbackPath(callbackPath))
        {
            errors.Add("CallbackPath must be an absolute path below /auth/oidc/, without a query, fragment, slash traversal, or duplicate slash");
        }

        if (errors.Count > 0)
        {
            throw new InvalidOperationException(
                $"Invalid Authentication:Oidc configuration: {string.Join("; ", errors)}.");
        }

        return new WorkforceOidcOptions(true, authority, clientId, clientSecret, displayName, callbackPath);
    }

    internal static bool TryNormalizeIssuer(string? value, out string? normalized)
    {
        normalized = null;
        if (string.IsNullOrWhiteSpace(value) ||
            !Uri.TryCreate(value, UriKind.Absolute, out var issuer) ||
            issuer.Scheme is not ("http" or "https") ||
            string.IsNullOrEmpty(issuer.Host) ||
            !string.IsNullOrEmpty(issuer.UserInfo) ||
            !string.IsNullOrEmpty(issuer.Query) ||
            !string.IsNullOrEmpty(issuer.Fragment))
        {
            return false;
        }

        // OIDC issuer comparison is case-insensitive for scheme/host, while the
        // issuer path remains case-sensitive. This is the logical provider key
        // used with the case-sensitive subject in Identity UserLogins.
        var scheme = issuer.Scheme.ToLowerInvariant();
        var host = issuer.Host.ToLowerInvariant();
        var port = issuer.IsDefaultPort ? string.Empty : $":{issuer.Port}";
        var path = issuer.AbsolutePath.TrimEnd('/');
        normalized = $"{scheme}://{host}{port}{path}";
        return normalized.Length <= 2048;
    }

    internal static string CreateOpaqueEmail(string issuer, string subject)
    {
        var bytes = SHA256.HashData(Encoding.UTF8.GetBytes($"{issuer}\0{subject}"));
        return $"{OpaqueEmailPrefix}{Convert.ToHexString(bytes).ToLowerInvariant()}@{OpaqueEmailDomain}";
    }

    internal static bool IsOpaqueEmail(string? email) =>
        email is not null &&
        email.StartsWith(OpaqueEmailPrefix, StringComparison.Ordinal) &&
        email.EndsWith($"@{OpaqueEmailDomain}", StringComparison.Ordinal) &&
        email.Length == OpaqueEmailPrefix.Length + 64 + 1 + OpaqueEmailDomain.Length;

    private static bool TryValidateAuthority(string value, IHostEnvironment environment, out string? error)
    {
        error = null;
        if (!Uri.TryCreate(value, UriKind.Absolute, out var authority) ||
            authority.Scheme is not ("http" or "https") ||
            !string.IsNullOrEmpty(authority.UserInfo) ||
            !string.IsNullOrEmpty(authority.Query) ||
            !string.IsNullOrEmpty(authority.Fragment) ||
            string.IsNullOrEmpty(authority.Host))
        {
            error = "Authority must be an absolute HTTP(S) issuer URL without user info, query, or fragment";
            return false;
        }

        if (!environment.IsDevelopment() && !string.Equals(authority.Scheme, Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase))
        {
            error = "Authority must use HTTPS outside Development";
            return false;
        }

        return true;
    }

    private static bool IsSafeCallbackPath(string path)
    {
        if (path.Length is 0 or > 200 ||
            !path.StartsWith("/auth/oidc/", StringComparison.Ordinal) ||
            path.Contains("//", StringComparison.Ordinal) ||
            path.Contains('\\') ||
            path.Contains('?') ||
            path.Contains('#') ||
            path.EndsWith("/", StringComparison.Ordinal))
        {
            return false;
        }

        var segments = path.Split('/', StringSplitOptions.RemoveEmptyEntries);
        return segments.All(segment => segment is not "." and not ".." &&
            segment.All(character => char.IsLetterOrDigit(character) || character is '-' or '_' or '.' or '~'));
    }
}