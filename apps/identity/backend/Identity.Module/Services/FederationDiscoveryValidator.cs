using System.Net;
using System.Net.Http.Headers;
using System.Net.Sockets;
using System.Text.Json;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public sealed record FederationDiscoveryValidationResult(
    bool Succeeded,
    string Code,
    string Message,
    string? Issuer = null,
    string? DiscoveryEndpoint = null,
    string? AuthorizationEndpoint = null,
    string? TokenEndpoint = null,
    string? JwksUri = null);

internal sealed record MetadataEndpointValidation(
    bool Succeeded,
    string? Value,
    FederationDiscoveryValidationResult? Result);

public interface IFederationDiscoveryValidator
{
    Task<FederationDiscoveryValidationResult> ValidateAsync(
        FederationProviderKind providerKind,
        string normalizedAuthority,
        CancellationToken cancellationToken);
}

public interface IFederationHostAddressResolver
{
    Task<IPAddress[]> ResolveAsync(string host, CancellationToken cancellationToken);
}

public sealed class DnsFederationHostAddressResolver : IFederationHostAddressResolver
{
    public Task<IPAddress[]> ResolveAsync(string host, CancellationToken cancellationToken) =>
        Dns.GetHostAddressesAsync(host, cancellationToken);
}

public sealed class FederationDiscoveryHttpMessageHandler : HttpClientHandler
{
    public FederationDiscoveryHttpMessageHandler()
    {
        AllowAutoRedirect = false;
        UseCookies = false;
        UseDefaultCredentials = false;
        Credentials = null;
        UseProxy = false;
    }
}

public sealed class OidcFederationDiscoveryValidator(
    IHttpClientFactory httpClientFactory,
    IFederationHostAddressResolver hostAddressResolver)
    : IFederationDiscoveryValidator
{
    public const string HttpClientName = "identity-federation-discovery";
    public async Task<FederationDiscoveryValidationResult> ValidateAsync(
        FederationProviderKind providerKind,
        string normalizedAuthority,
        CancellationToken cancellationToken)
    {
        if (!Uri.TryCreate(normalizedAuthority.TrimEnd('/') + "/.well-known/openid-configuration", UriKind.Absolute, out var endpoint) ||
            endpoint.Scheme != Uri.UriSchemeHttps ||
            !string.IsNullOrEmpty(endpoint.UserInfo) ||
            !string.IsNullOrEmpty(endpoint.Query) ||
            !string.IsNullOrEmpty(endpoint.Fragment))
            return Failure("invalid_discovery_endpoint", "The configured authority cannot be used for OIDC discovery.");

        var providerError = ValidateProviderAuthority(providerKind, normalizedAuthority);
        if (providerError is not null) return providerError;

        var hostError = await ValidatePublicHostAsync(endpoint.DnsSafeHost, cancellationToken);
        if (hostError is not null) return hostError;

        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, endpoint);
            request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
            using var response = await httpClientFactory.CreateClient(HttpClientName)
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken);

            if ((int)response.StatusCode is >= 300 and < 400)
                return Failure("discovery_redirect_not_allowed", "The discovery endpoint must not redirect.");
            if (!response.IsSuccessStatusCode)
                return Failure("discovery_request_failed", "The OIDC discovery document could not be retrieved.");

            var mediaType = response.Content.Headers.ContentType?.MediaType;
            if (!string.Equals(mediaType, "application/json", StringComparison.OrdinalIgnoreCase))
                return Failure("discovery_content_type_invalid", "The OIDC discovery document did not use the required JSON content type.");
            using var document = JsonDocument.Parse(await FederationEndpointPolicy.ReadBoundedAsync(response.Content, cancellationToken));
            if (!document.RootElement.TryGetProperty("issuer", out var issuerElement) ||
                issuerElement.ValueKind != JsonValueKind.String ||
                string.IsNullOrWhiteSpace(issuerElement.GetString()))
                return Failure("discovery_issuer_missing", "The OIDC discovery document did not contain an issuer.");

            var issuer = issuerElement.GetString()!;
            if (!IsValidHttpsEndpoint(issuer))
                return Failure("discovery_issuer_invalid", "The OIDC discovery issuer is invalid.");
            // The API persists a canonical authority (lower-case host and no
            // trailing slash). Compare the discovery issuer byte-for-byte with
            // that canonical value; do not apply URI normalization here.
            if (!string.Equals(issuer, normalizedAuthority, StringComparison.Ordinal))
                return Failure("discovery_issuer_mismatch", "The OIDC discovery issuer does not match the configured authority.");

            var authorizationEndpoint = await ReadAndValidateMetadataEndpointAsync(document.RootElement, "authorization_endpoint", cancellationToken);
            if (authorizationEndpoint is null) return Failure("discovery_authorization_endpoint_missing", "The OIDC discovery document did not contain a safe authorization endpoint.");
            if (!authorizationEndpoint.Succeeded) return authorizationEndpoint.Result!;

            var tokenEndpoint = await ReadAndValidateMetadataEndpointAsync(document.RootElement, "token_endpoint", cancellationToken);
            if (tokenEndpoint is null) return Failure("discovery_token_endpoint_missing", "The OIDC discovery document did not contain a safe token endpoint.");
            if (!tokenEndpoint.Succeeded) return tokenEndpoint.Result!;

            var jwksUri = await ReadAndValidateMetadataEndpointAsync(document.RootElement, "jwks_uri", cancellationToken);
            if (jwksUri is null) return Failure("discovery_jwks_uri_missing", "The OIDC discovery document did not contain a safe JWKS URI.");
            if (!jwksUri.Succeeded) return jwksUri.Result!;

            return new(true, "validated", "The OIDC discovery document was validated.", issuer, endpoint.ToString(),
                authorizationEndpoint.Value, tokenEndpoint.Value, jwksUri.Value);
        }
        catch (InvalidDataException)
        {
            return Failure("discovery_response_too_large", "The OIDC discovery document is too large.");
        }
        catch (OperationCanceledException)
        {
            return Failure("discovery_timeout", "The OIDC discovery request timed out.");
        }
        catch (HttpRequestException)
        {
            return Failure("discovery_request_failed", "The OIDC discovery document could not be retrieved.");
        }
        catch (JsonException)
        {
            return Failure("discovery_json_invalid", "The OIDC discovery document was not valid JSON.");
        }
    }

    private async Task<MetadataEndpointValidation?> ReadAndValidateMetadataEndpointAsync(
        JsonElement root,
        string propertyName,
        CancellationToken cancellationToken)
    {
        if (!root.TryGetProperty(propertyName, out var value) ||
            value.ValueKind != JsonValueKind.String || string.IsNullOrWhiteSpace(value.GetString()))
            return null;
        var endpoint = value.GetString()!;
        if (!IsValidHttpsEndpoint(endpoint) || !Uri.TryCreate(endpoint, UriKind.Absolute, out var uri))
            return new(false, null, Failure("discovery_metadata_endpoint_invalid", $"The OIDC discovery {propertyName} is invalid."));
        var hostError = await ValidatePublicHostAsync(uri.DnsSafeHost, cancellationToken);
        return hostError is null
            ? new(true, endpoint, null)
            : new(false, null, Failure("discovery_metadata_endpoint_not_public", $"The OIDC discovery {propertyName} must use a public host."));
    }

    private async Task<FederationDiscoveryValidationResult?> ValidatePublicHostAsync(string host, CancellationToken cancellationToken)
    {
        if (IPAddress.TryParse(host, out var literal))
            return FederationEndpointPolicy.IsPublicAddress(literal) ? null : Failure("discovery_host_not_public", "The discovery host must resolve only to public addresses.");

        IPAddress[] addresses;
        try { addresses = await hostAddressResolver.ResolveAsync(host, cancellationToken); }
        catch (SocketException) { return Failure("discovery_host_unresolved", "The discovery host could not be resolved."); }

        return addresses.Length > 0 && addresses.All(FederationEndpointPolicy.IsPublicAddress)
            ? null
            : Failure("discovery_host_not_public", "The discovery host must resolve only to public addresses.");
    }

    private static FederationDiscoveryValidationResult? ValidateProviderAuthority(FederationProviderKind providerKind, string authority)
    {
        if (!Uri.TryCreate(authority, UriKind.Absolute, out var uri))
            return Failure("invalid_authority", "The configured authority is invalid.");

        if (providerKind == FederationProviderKind.Google &&
            (!string.Equals(uri.Host, "accounts.google.com", StringComparison.OrdinalIgnoreCase) || uri.AbsolutePath.Trim('/').Length != 0))
            return Failure("provider_authority_mismatch", "Google connections must use the accounts.google.com issuer.");

        if (providerKind == FederationProviderKind.Entra)
        {
            var knownHost = uri.Host.Equals("login.microsoftonline.com", StringComparison.OrdinalIgnoreCase) ||
                uri.Host.Equals("login.microsoftonline.us", StringComparison.OrdinalIgnoreCase) ||
                uri.Host.Equals("login.microsoftonline.de", StringComparison.OrdinalIgnoreCase) ||
                uri.Host.Equals("login.chinacloudapi.cn", StringComparison.OrdinalIgnoreCase);
            var tenant = uri.AbsolutePath.Trim('/').Split('/', StringSplitOptions.RemoveEmptyEntries).FirstOrDefault();
            if (!knownHost || string.IsNullOrWhiteSpace(tenant) ||
                tenant.Equals("common", StringComparison.OrdinalIgnoreCase) ||
                tenant.Equals("organizations", StringComparison.OrdinalIgnoreCase) ||
                tenant.Equals("consumers", StringComparison.OrdinalIgnoreCase))
                return Failure("provider_authority_mismatch", "Entra connections must use a tenant-specific Microsoft issuer.");
        }

        return null;
    }

    private static bool IsValidHttpsEndpoint(string value) =>
        FederationEndpointPolicy.TryValidateHttpsEndpoint(value, out _);

    private static FederationDiscoveryValidationResult Failure(string code, string message) => new(false, code, message);
}