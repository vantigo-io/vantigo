using System.Security.Cryptography;
using System.Text;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public sealed record ScimTokenVerificationResult(bool Succeeded, Guid? ScimConnectionId);

/// <summary>Validates the deployment-bound static SCIM credential.</summary>
public sealed class ScimTokenService(StaticScimOptions options, TimeProvider? timeProvider = null)
{
    private TimeProvider Clock => timeProvider ?? TimeProvider.System;

    public Task<ScimTokenVerificationResult> VerifyAsync(string? plaintext, CancellationToken cancellationToken)
    {
        if (!options.Enabled || string.IsNullOrWhiteSpace(plaintext) || plaintext.Any(char.IsWhiteSpace))
            return Task.FromResult(new ScimTokenVerificationResult(false, null));

        var current = FixedEquals(plaintext, options.BearerToken);
        var previous = options.PreviousBearerToken is not null &&
            options.PreviousBearerTokenExpiresAtUtc > Clock.GetUtcNow() &&
            FixedEquals(plaintext, options.PreviousBearerToken);
        return Task.FromResult(current || previous
            ? new ScimTokenVerificationResult(true, ScimConnection.StaticId)
            : new ScimTokenVerificationResult(false, null));
    }

    private static bool FixedEquals(string supplied, string? configured)
    {
        if (configured is null) return false;
        var actual = Encoding.UTF8.GetBytes(supplied);
        var expected = Encoding.UTF8.GetBytes(configured);
        return actual.Length == expected.Length && CryptographicOperations.FixedTimeEquals(actual, expected);
    }
}