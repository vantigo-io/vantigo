using Microsoft.AspNetCore.Hosting;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>
/// The one optional workforce OpenID Connect provider supported by the host. It
/// is deliberately configuration-only: no provider settings are persisted in the
/// accounts database and no external provider claims are copied to local accounts.
/// </summary>
public sealed record WorkforceOidcOptions(
    bool Enabled,
    string Authority,
    string ClientId,
    string ClientSecret,
    string DisplayName,
    string CallbackPath)
{
    public const string Scheme = "VantigoWorkforceOidc";

    // The OIDC handler's default claim actions may remove protocol claims such as
    // iss. This private claim is populated only after the handler validates the
    // token and is the sole issuer value consumed by completion.
    public const string ValidatedIssuerClaim = "vantigo:oidc:validated-issuer";

    public const string CompletionPath = "/api/v1/identity/oidc/complete";

    public const string DefaultCallbackPath = "/api/v1/identity/oidc/callback";

    private const string OpaqueEmailPrefix = "oidc-";
    private const string OpaqueEmailDomain = "sso.invalid";

    private static readonly string[] ReservedCallbackPaths =
    [
        "/api/v1/identity/providers",
        "/api/v1/identity/oidc/challenge",
        CompletionPath,
    ];

    public static WorkforceOidcOptions Disabled { get; } = new(
        false,
        string.Empty,
        string.Empty,
        string.Empty,
        "Workforce SSO",
        DefaultCallbackPath);

    /// <summary>
    /// Validates the raw <see cref="VantigoAuthenticationOptions.Oidc"/> values
    /// and returns either an enabled provider or <see cref="Disabled"/>.
    /// </summary>
    /// <exception cref="InvalidOperationException">
    /// Partial OIDC configuration is supplied and fails validation.
    /// </exception>
    public static WorkforceOidcOptions FromAuthenticationOptions(
        VantigoAuthenticationOptions options,
        IHostEnvironment environment)
    {
        var oidc = options.Oidc;
        var authority = oidc.Authority?.Trim() ?? string.Empty;
        var clientId = oidc.ClientId?.Trim() ?? string.Empty;
        var clientSecret = oidc.ClientSecret?.Trim() ?? string.Empty;
        var displayName = oidc.DisplayName?.Trim() ?? string.Empty;
        var callbackPath = oidc.CallbackPath?.Trim() ?? string.Empty;

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
            errors.Add("CallbackPath must be an absolute path below /api/v1/identity/oidc/, without a query, fragment, slash traversal, or duplicate slash");
        }

        if (errors.Count > 0)
        {
            throw new InvalidOperationException(
                $"Invalid Authentication:Oidc configuration: {string.Join("; ", errors)}.");
        }

        return new WorkforceOidcOptions(true, authority, clientId, clientSecret, displayName, callbackPath);
    }

    public static bool TryNormalizeIssuer(string? value, out string? normalized)
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
        var port = issuer.IsDefaultPort ? string.Empty : $":" + issuer.Port;
        var path = issuer.AbsolutePath.TrimEnd('/');
        normalized = $"{scheme}://{host}{port}{path}";
        return normalized.Length <= 2048;
    }

    public static string CreateOpaqueEmail(string issuer, string subject)
    {
        var bytes = System.Security.Cryptography.SHA256.HashData(System.Text.Encoding.UTF8.GetBytes($"{issuer}\0{subject}"));
        return $"{OpaqueEmailPrefix}{Convert.ToHexString(bytes).ToLowerInvariant()}@{OpaqueEmailDomain}";
    }

    public static bool IsOpaqueEmail(string? email) =>
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
            !path.StartsWith("/api/v1/identity/oidc/", StringComparison.Ordinal) ||
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

/// <summary>
/// Resolves the effective <see cref="WorkforceOidcOptions"/> from the configured
/// Vantigo authentication options at startup. This validates configuration eagerly
/// so misconfiguration surfaces immediately.
/// </summary>
public sealed class WorkforceOidcOptionsResolver
{
    public WorkforceOidcOptionsResolver(IOptions<VantigoAuthenticationOptions> options, IHostEnvironment environment)
    {
        Value = WorkforceOidcOptions.FromAuthenticationOptions(options.Value, environment);
    }

    public WorkforceOidcOptions Value { get; }
}

public static class WorkforceOidcConfigurationExtensions
{
    /// <summary>
    /// Registers a startup-resolved <see cref="WorkforceOidcOptions"/> singleton
    /// from <see cref="VantigoAuthenticationOptions"/> and the current hosting
    /// environment.
    /// </summary>
    public static IServiceCollection AddWorkforceOidcOptions(this IServiceCollection services)
    {
        services.AddSingleton<WorkforceOidcOptionsResolver>();
        services.AddSingleton(serviceProvider => serviceProvider.GetRequiredService<WorkforceOidcOptionsResolver>().Value);
        return services;
    }
}