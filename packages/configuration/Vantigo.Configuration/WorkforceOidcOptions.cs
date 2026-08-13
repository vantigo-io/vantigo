using System.Text;
using System.Text.RegularExpressions;

using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>
/// The one startup-bound workforce OpenID Connect provider supported by the host.
/// </summary>
public sealed record WorkforceOidcOptions(
    bool Enabled,
    string Provider,
    string Authority,
    string ClientId,
    string ClientAuthentication,
    string? ClientSecret,
    string? WorkloadIdentityTokenFile,
    string[] AllowedDomains,
    string DisplayName,
    string CallbackPath,
    string? TenantId)
{
    public const string Scheme = "VantigoWorkforceOidc";
    public const string ValidatedIssuerClaim = "vantigo:oidc:validated-issuer";
    public const string CompletionPath = "/api/v1/identity/oidc/complete";
    public const string DefaultCallbackPath = "/api/v1/identity/oidc/callback";
    public const string EntraProvider = "Entra";
    public const string GoogleProvider = "Google";
    public const string ClientSecretAuthentication = "ClientSecret";
    public const string WorkloadIdentityAuthentication = "WorkloadIdentity";

    private const string OpaqueEmailPrefix = "oidc-";
    private const string OpaqueEmailDomain = "sso.invalid";

    public static WorkforceOidcOptions Disabled { get; } = new(
        false, string.Empty, string.Empty, string.Empty, string.Empty, null, null, [],
        "Workforce SSO", DefaultCallbackPath, null);

    public static WorkforceOidcOptions FromAuthenticationOptions(
        VantigoAuthenticationOptions options,
        IHostEnvironment environment)
    {
        var oidc = options.Oidc;
        var authority = oidc.Authority?.Trim() ?? string.Empty;
        var clientId = oidc.ClientId?.Trim() ?? string.Empty;
        var provider = oidc.Provider?.Trim() ?? string.Empty;
        var clientAuthentication = oidc.ClientAuthentication?.Trim() ?? string.Empty;
        var displayName = oidc.DisplayName?.Trim() ?? string.Empty;
        var callbackPath = oidc.CallbackPath?.Trim() ?? string.Empty;

        if (!oidc.Enabled)
        {
            if (!string.IsNullOrEmpty(callbackPath) && !string.Equals(callbackPath, DefaultCallbackPath, StringComparison.Ordinal))
                throw new InvalidOperationException($"Invalid Authentication:Oidc configuration: CallbackPath is fixed at {DefaultCallbackPath}");
            if (!string.IsNullOrEmpty(provider) || !string.IsNullOrEmpty(authority) ||
                !string.IsNullOrEmpty(clientId) || !string.IsNullOrEmpty(clientAuthentication) ||
                !string.IsNullOrEmpty(oidc.ClientSecret) || !string.IsNullOrEmpty(oidc.WorkloadIdentityTokenFile) ||
                oidc.AllowedDomains is { Length: > 0 })
                throw new InvalidOperationException("Authentication:Oidc contains provider settings but Enabled is false.");
            return Disabled;
        }

        var errors = new List<string>();
        if (!provider.Equals(EntraProvider, StringComparison.OrdinalIgnoreCase) &&
            !provider.Equals(GoogleProvider, StringComparison.OrdinalIgnoreCase))
            errors.Add("Provider must be Entra or Google");
        else
            provider = provider.Equals(EntraProvider, StringComparison.OrdinalIgnoreCase) ? EntraProvider : GoogleProvider;

        string? tenantId = null;
        if (string.IsNullOrWhiteSpace(authority))
            errors.Add("Authority is required");
        else if (provider == EntraProvider)
        {
            if (!TryValidateEntraAuthority(authority, out tenantId))
                errors.Add("Entra Authority must be exactly https://login.microsoftonline.com/<tenant-guid>/v2.0");
        }
        else if (!string.Equals(authority, "https://accounts.google.com", StringComparison.Ordinal))
            errors.Add("Google Authority must be exactly https://accounts.google.com");

        if (string.IsNullOrWhiteSpace(clientId))
            errors.Add("ClientId is required");
        else if (clientId.Length > 256 || clientId.Any(char.IsControl) || clientId.Any(char.IsWhiteSpace))
            errors.Add("ClientId must be a safe value");
        else if (provider == EntraProvider && !Guid.TryParse(clientId, out _))
            errors.Add("Entra ClientId must be a GUID");
        else if (provider == GoogleProvider && !clientId.EndsWith(".apps.googleusercontent.com", StringComparison.Ordinal))
            errors.Add("Google ClientId must end with .apps.googleusercontent.com");

        if (!clientAuthentication.Equals(ClientSecretAuthentication, StringComparison.OrdinalIgnoreCase) &&
            !clientAuthentication.Equals(WorkloadIdentityAuthentication, StringComparison.OrdinalIgnoreCase))
            errors.Add("ClientAuthentication must be ClientSecret or WorkloadIdentity");
        else
            clientAuthentication = clientAuthentication.Equals(ClientSecretAuthentication, StringComparison.OrdinalIgnoreCase)
                ? ClientSecretAuthentication : WorkloadIdentityAuthentication;

        var secret = oidc.ClientSecret;
        var tokenFile = oidc.WorkloadIdentityTokenFile;
        if (clientAuthentication == ClientSecretAuthentication)
        {
            if (string.IsNullOrEmpty(secret)) errors.Add("ClientSecret is required for ClientSecret authentication");
            if (!string.IsNullOrEmpty(tokenFile)) errors.Add("WorkloadIdentityTokenFile is not allowed for ClientSecret authentication");
        }
        else
        {
            if (provider != EntraProvider) errors.Add("WorkloadIdentity authentication is supported only for Entra");
            if (!string.IsNullOrEmpty(secret)) errors.Add("ClientSecret is not allowed for WorkloadIdentity authentication");
            tokenFile = string.IsNullOrWhiteSpace(tokenFile)
                ? Environment.GetEnvironmentVariable("AZURE_FEDERATED_TOKEN_FILE")
                : tokenFile;
            if (string.IsNullOrWhiteSpace(tokenFile) || !Path.IsPathRooted(tokenFile) || !File.Exists(tokenFile))
                errors.Add("WorkloadIdentityTokenFile must be an absolute readable file");
            else
            {
                try
                {
                    using var stream = new FileStream(tokenFile, FileMode.Open, FileAccess.Read, FileShare.Read);
                }
                catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
                {
                    errors.Add("WorkloadIdentityTokenFile must be an absolute readable file");
                }
            }
        }

        var domains = NormalizeDomains(oidc.AllowedDomains, errors);
        if (provider == EntraProvider && domains.Length > 0)
            errors.Add("AllowedDomains is only valid for Google");
        if (provider == GoogleProvider && domains.Length == 0)
            errors.Add("Google requires at least one AllowedDomains value");

        if (displayName.Length > 100 || displayName.Any(char.IsControl))
            errors.Add("DisplayName must be at most 100 characters and contain no control characters");
        displayName = string.IsNullOrWhiteSpace(displayName) ? "Workforce SSO" : displayName;

        if (!string.IsNullOrEmpty(callbackPath) && !string.Equals(callbackPath, DefaultCallbackPath, StringComparison.Ordinal))
            errors.Add($"CallbackPath is fixed at {DefaultCallbackPath}");

        if (errors.Count > 0)
            throw new InvalidOperationException($"Invalid Authentication:Oidc configuration: {string.Join("; ", errors)}.");

        return new(true, provider, authority, clientId, clientAuthentication,
            clientAuthentication == ClientSecretAuthentication ? secret : null,
            clientAuthentication == WorkloadIdentityAuthentication ? tokenFile : null,
            domains, displayName, DefaultCallbackPath, tenantId);
    }

    public static bool TryValidateEntraAuthority(string value, out string? tenantId)
    {
        tenantId = null;
        var match = Regex.Match(value, "^https://login\\.microsoftonline\\.com/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})/v2\\.0$", RegexOptions.CultureInvariant);
        if (!match.Success || !Guid.TryParse(match.Groups[1].Value, out var parsed)) return false;
        tenantId = parsed.ToString("D");
        return true;
    }

    public static bool TryNormalizeIssuer(string? value, out string? normalized)
    {
        normalized = null;
        if (string.IsNullOrWhiteSpace(value) || !Uri.TryCreate(value, UriKind.Absolute, out var issuer) ||
            issuer.Scheme is not ("http" or "https") || string.IsNullOrEmpty(issuer.Host) ||
            !string.IsNullOrEmpty(issuer.UserInfo) || !string.IsNullOrEmpty(issuer.Query) ||
            !string.IsNullOrEmpty(issuer.Fragment)) return false;
        normalized = $"{issuer.Scheme.ToLowerInvariant()}://{issuer.Host.ToLowerInvariant()}" +
            $"{(issuer.IsDefaultPort ? string.Empty : ":" + issuer.Port)}{issuer.AbsolutePath.TrimEnd('/')}";
        return normalized.Length <= 2048;
    }

    public static string CreateOpaqueEmail(string issuer, string subject)
    {
        var bytes = System.Security.Cryptography.SHA256.HashData(Encoding.UTF8.GetBytes($"{issuer}\0{subject}"));
        return $"{OpaqueEmailPrefix}{Convert.ToHexString(bytes).ToLowerInvariant()}@{OpaqueEmailDomain}";
    }

    public static bool IsOpaqueEmail(string? email) => email is not null &&
        email.StartsWith(OpaqueEmailPrefix, StringComparison.Ordinal) &&
        email.EndsWith($"@{OpaqueEmailDomain}", StringComparison.Ordinal) &&
        email.Length == OpaqueEmailPrefix.Length + 64 + 1 + OpaqueEmailDomain.Length;

    private static string[] NormalizeDomains(IEnumerable<string>? values, List<string> errors)
    {
        var result = new HashSet<string>(StringComparer.Ordinal);
        foreach (var value in values ?? [])
        {
            var domain = value.Trim().TrimEnd('.').ToLowerInvariant();
            if (domain.Length is 0 or > 253 || domain.Contains('/') || domain.Contains('@') ||
                domain.Contains(':') || domain.Any(char.IsWhiteSpace) ||
                !Regex.IsMatch(domain, @"^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$", RegexOptions.CultureInvariant))
            {
                errors.Add("AllowedDomains must contain bare DNS names");
                continue;
            }
            result.Add(domain);
        }
        return result.Order(StringComparer.Ordinal).ToArray();
    }
}

public sealed class WorkforceOidcOptionsResolver
{
    public WorkforceOidcOptionsResolver(IOptions<VantigoAuthenticationOptions> options, IHostEnvironment environment) =>
        Value = WorkforceOidcOptions.FromAuthenticationOptions(options.Value, environment);

    public WorkforceOidcOptions Value { get; }
}

public static class WorkforceOidcConfigurationExtensions
{
    public static IServiceCollection AddWorkforceOidcOptions(this IServiceCollection services)
    {
        services.AddSingleton<WorkforceOidcOptionsResolver>();
        services.AddSingleton(serviceProvider => serviceProvider.GetRequiredService<WorkforceOidcOptionsResolver>().Value);
        return services;
    }
}