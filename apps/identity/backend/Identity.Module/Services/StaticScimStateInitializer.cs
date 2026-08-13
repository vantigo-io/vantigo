using Microsoft.EntityFrameworkCore;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

/// <summary>
/// Materializes one deterministic static SCIM connection so protocol mappings,
/// groups, and lifecycle state remain durable without a provider relationship.
/// Static SCIM is the only provisioning scope in this phase.
/// </summary>
public sealed class StaticScimStateInitializer(
    AccountsDbContext dbContext,
    StaticScimOptions options)
{
    public async Task EnsureAsync(CancellationToken cancellationToken = default)
    {
        if (!options.Enabled) return;

        var now = DateTimeOffset.UtcNow;
        var connection = await dbContext.ScimConnections.SingleOrDefaultAsync(
            item => item.Id == ScimConnection.StaticId, cancellationToken);
        if (connection is null)
        {
            connection = new ScimConnection
            {
                Id = ScimConnection.StaticId,
                CreatedAt = now,
                UpdatedAt = now,
            };
            dbContext.ScimConnections.Add(connection);
        }
        await dbContext.SaveChangesAsync(cancellationToken);
    }

}