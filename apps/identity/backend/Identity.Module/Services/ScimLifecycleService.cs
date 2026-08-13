using Microsoft.EntityFrameworkCore;

using Vantigo.Configuration;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public sealed class ScimLifecycleService(AccountsDbContext dbContext, StaticScimOptions options)
{
    public async Task<bool> IsEffectivelyDisabledAsync(Guid userId, CancellationToken cancellationToken)
    {
        var user = await dbContext.Users.AsNoTracking().SingleOrDefaultAsync(item => item.Id == userId, cancellationToken);
        if (user is null || user.IsDisabled) return true;

        var ownerRoleId = await dbContext.Roles.AsNoTracking()
            .Where(role => role.Name == AuthRoles.Owner)
            .Select(role => (Guid?)role.Id)
            .SingleOrDefaultAsync(cancellationToken);
        if (ownerRoleId.HasValue && await dbContext.UserRoles.AsNoTracking()
                .AnyAsync(assignment => assignment.UserId == userId && assignment.RoleId == ownerRoleId.Value, cancellationToken))
            return false;

        return options.Enabled && await dbContext.ScimUserMappings.AsNoTracking()
            .AnyAsync(mapping => mapping.ScimConnectionId == ScimConnection.StaticId &&
                mapping.UserId == userId && !mapping.UpstreamActive, cancellationToken);
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