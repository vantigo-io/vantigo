using System.Security.Cryptography;
using System.Text;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public sealed record ScimTokenVerificationResult(bool Succeeded, Guid? ScimConnectionId, int? Version);

public interface IScimTokenPepper
{
    byte[] GetPepper();
}

public sealed class EnvironmentScimTokenPepper(IConfiguration configuration) : IScimTokenPepper
{
    public byte[] GetPepper()
    {
        var reference = configuration["Authentication:Scim:TokenPepperReference"];
        if (string.IsNullOrWhiteSpace(reference) ||
            !System.Text.RegularExpressions.Regex.IsMatch(reference,
                @"^VANTIGO_SCIM_[A-Z0-9]+(?:_[A-Z0-9]+)*_TOKEN_PEPPER$",
                System.Text.RegularExpressions.RegexOptions.CultureInvariant))
            throw new InvalidOperationException("Authentication:Scim:TokenPepperReference must reference a federation environment secret.");
        var value = Environment.GetEnvironmentVariable(reference);
        if (string.IsNullOrWhiteSpace(value)) throw new InvalidOperationException("The SCIM token pepper is not configured.");
        return Encoding.UTF8.GetBytes(value);
    }
}

public sealed class ScimTokenService(
    AccountsDbContext dbContext,
    IScimTokenPepper pepper,
    TimeProvider? timeProvider = null)
{
    private const int TokenBytes = 32;
    public static readonly TimeSpan RotationOverlap = TimeSpan.FromMinutes(10);

    private TimeProvider Clock => timeProvider ?? TimeProvider.System;

    public async Task<(ScimBearerToken Token, string Plaintext)> CreateAsync(
        Guid connectionId, int version, DateTimeOffset now, CancellationToken cancellationToken)
    {
        var plaintext = Convert.ToBase64String(RandomNumberGenerator.GetBytes(TokenBytes));
        var token = new ScimBearerToken
        {
            ScimConnectionId = connectionId,
            Version = version,
            TokenHash = Hash(plaintext),
            CreatedAt = now,
            IsCurrent = true,
        };
        dbContext.ScimBearerTokens.Add(token);
        await dbContext.SaveChangesAsync(cancellationToken);
        return (token, plaintext);
    }

    public async Task<ScimTokenVerificationResult> VerifyAsync(string? plaintext, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(plaintext)) return new(false, null, null);
        var hash = Hash(plaintext);
        var now = Clock.GetUtcNow();
        var activeTokens = await dbContext.ScimBearerTokens.AsNoTracking()
            .Where(item => item.RevokedAt == null)
            .Join(dbContext.ScimConnections.AsNoTracking().Where(connection => connection.IsEnabled),
                item => item.ScimConnectionId, connection => connection.Id, (item, connection) => item)
            .ToListAsync(cancellationToken);

        // A rotation leaves exactly one non-current token eligible for the
        // overlap window. Select the newest such token defensively as well, so
        // a malformed/legacy row cannot turn every non-current token into a
        // valid bearer credential.
        var candidates = activeTokens
            .GroupBy(item => item.ScimConnectionId)
            .SelectMany(group => group.Where(item => item.IsCurrent)
                .Concat(group.Where(item => !item.IsCurrent && item.ExpiresAt > now)
                    .OrderByDescending(item => item.CreatedAt)
                    .Take(1)))
            .Where(item => item.IsCurrent
                ? item.ExpiresAt is null || item.ExpiresAt > now
                : item.ExpiresAt > now);

        foreach (var candidate in candidates)
        {
            var expected = Convert.FromHexString(candidate.TokenHash);
            var actual = Convert.FromHexString(hash);
            if (CryptographicOperations.FixedTimeEquals(expected, actual))
                return new(true, candidate.ScimConnectionId, candidate.Version);
        }
        return new(false, null, null);
    }

    private string Hash(string plaintext)
    {
        using var hmac = new HMACSHA256(pepper.GetPepper());
        return Convert.ToHexString(hmac.ComputeHash(Encoding.UTF8.GetBytes(plaintext))).ToLowerInvariant();
    }
}