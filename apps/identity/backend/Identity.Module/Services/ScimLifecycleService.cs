using Microsoft.EntityFrameworkCore;

using Vantigo.Configuration;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public sealed class ScimLifecycleService(AccountsDbContext dbContext, StaticScimOptions options)
{
    public async Task<bool> IsEffectivelyDisabledAsync(Guid userId, CancellationToken cancellationToken) =>
        await LoadSessionStateAsync(userId, cancellationToken) is not { IsEffectivelyDisabled: false };

    /// <summary>
    /// Reads everything the cookie validation path needs about an account in a
    /// single round trip: the security stamp that authorizes the session and the
    /// effective disabled state. Cookie validation runs on every request, so the
    /// disabled, Owner break-glass, and SCIM upstream checks are one projection
    /// rather than three sequential queries. Returns null when the account is gone.
    /// </summary>
    public async Task<AccountSessionState?> LoadSessionStateAsync(Guid userId, CancellationToken cancellationToken)
    {
        var state = await dbContext.Users.AsNoTracking()
            .Where(user => user.Id == userId)
            .Select(user => new
            {
                user.SecurityStamp,
                user.IsDisabled,
                IsOwner = dbContext.UserRoles.Any(assignment => assignment.UserId == user.Id &&
                    dbContext.Roles.Any(role => role.Id == assignment.RoleId && role.Name == AuthRoles.Owner)),
                ScimUpstreamInactive = dbContext.ScimUserMappings.Any(mapping =>
                    mapping.ScimConnectionId == ScimConnection.StaticId &&
                    mapping.UserId == user.Id && !mapping.UpstreamActive),
            })
            .SingleOrDefaultAsync(cancellationToken);
        if (state is null) return null;

        // An Owner stays available even when the upstream directory deactivates
        // the account: it is the break-glass account. An explicit administrative
        // disable still applies to everyone.
        bool effectivelyDisabled = state.IsDisabled ||
            (!state.IsOwner && options.Enabled && state.ScimUpstreamInactive);
        return new AccountSessionState(state.SecurityStamp, effectivelyDisabled);
    }

    public async Task<bool> IsScimControllableUserAsync(Guid userId, CancellationToken cancellationToken)
    {
        var ownerRoleId = await dbContext.Roles.AsNoTracking()
            .Where(role => role.Name == AuthRoles.Owner)
            .Select(role => (Guid?)role.Id)
            .SingleOrDefaultAsync(cancellationToken);
        return !ownerRoleId.HasValue || !await dbContext.UserRoles.AsNoTracking()
            .AnyAsync(assignment => assignment.UserId == userId && assignment.RoleId == ownerRoleId.Value, cancellationToken);
    }
}

/// <summary>The account state a cookie session is validated against.</summary>
public sealed record AccountSessionState(string? SecurityStamp, bool IsEffectivelyDisabled);