using Microsoft.Extensions.Configuration;

namespace Vantigo.Identity.Services;

public enum FederationClientSecretResolutionStatus
{
    Missing,
    Configured,
    InvalidReference
}

public sealed record FederationClientSecretResolution(
    FederationClientSecretResolutionStatus Status,
    string? Secret);

public interface IFederationClientSecretResolver
{
    FederationClientSecretResolution Resolve(string reference);
}

/// <summary>
/// Resolves a deployment-owned configuration secret at the point it is needed.
/// The resolved value is never part of a federation response or audit projection.
/// </summary>
public sealed class ConfigurationFederationClientSecretResolver(IConfiguration configuration)
    : IFederationClientSecretResolver
{
    public FederationClientSecretResolution Resolve(string reference)
    {
        if (!FederationConnectionRules.TryNormalizeClientSecretReference(reference, out var normalized))
            return new(FederationClientSecretResolutionStatus.InvalidReference, null);

        // Deliberately read the exact process environment variable. Do not use
        // IConfiguration's indexer here: arbitrary providers could alias a
        // database/configuration key to the reference and turn it into a secret
        // source outside the documented deployment contract.
        _ = configuration;
        var value = Environment.GetEnvironmentVariable(normalized!);
        return string.IsNullOrWhiteSpace(value)
            ? new(FederationClientSecretResolutionStatus.Missing, null)
            : new(FederationClientSecretResolutionStatus.Configured, value);
    }
}

internal static class FederationConnectionRules
{
    public static bool TryNormalizeClientSecretReference(string? value, out string? normalized)
    {
        normalized = null;
        if (string.IsNullOrWhiteSpace(value)) return false;
        var candidate = value;
        if (candidate.Length > 256 || candidate.Any(char.IsWhiteSpace) || candidate.Any(char.IsControl) ||
            !System.Text.RegularExpressions.Regex.IsMatch(
                candidate,
                @"^VANTIGO_SSO_[A-Z0-9]+(?:_[A-Z0-9]+)*_CLIENT_SECRET$",
                System.Text.RegularExpressions.RegexOptions.CultureInvariant))
            return false;
        normalized = candidate;
        return true;
    }
}