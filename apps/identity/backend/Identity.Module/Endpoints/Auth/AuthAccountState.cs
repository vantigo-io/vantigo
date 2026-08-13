using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Endpoints.Auth;

internal static class AuthAccountState
{
    internal const int OwnerMutationLockKey = 0x56414e54;

    internal static bool IsLockedOut(DateTimeOffset? lockoutEnd, DateTimeOffset now) =>
        lockoutEnd.HasValue && lockoutEnd.Value > now;

    internal static bool IsActive(ApplicationUser user, DateTimeOffset now) =>
        !user.IsDisabled && !IsLockedOut(user.LockoutEnd, now);

    internal static bool IsUnavailable(ApplicationUser user, DateTimeOffset now) =>
        user.IsDisabled || IsLockedOut(user.LockoutEnd, now);

    internal static Task AcquireOwnerMutationLock(
        AccountsDbContext dbContext,
        CancellationToken cancellationToken) =>
        dbContext.Database.ExecuteSqlRawAsync(
            "SELECT pg_advisory_xact_lock(CAST({0} AS bigint))",
            [(object)(long)OwnerMutationLockKey], cancellationToken);

    internal static async Task<bool> IsLastActiveOwner(
        AccountsDbContext dbContext,
        Guid targetId,
        DateTimeOffset now,
        CancellationToken cancellationToken)
    {
        var ownerRoleId = await dbContext.Roles
            .Where(role => role.Name == AuthRoles.Owner)
            .Select(role => (Guid?)role.Id)
            .SingleOrDefaultAsync(cancellationToken);
        if (!ownerRoleId.HasValue)
        {
            return false;
        }

        var targetIsActiveOwner = await dbContext.UserRoles
            .Join(dbContext.Users, assignment => assignment.UserId, user => user.Id,
                (assignment, user) => new { assignment, user })
            .Where(item => item.assignment.RoleId == ownerRoleId.Value &&
                item.user.Id == targetId && !item.user.IsDisabled &&
                (!item.user.LockoutEnd.HasValue || item.user.LockoutEnd <= now))
            .AnyAsync(cancellationToken);
        if (!targetIsActiveOwner)
        {
            return false;
        }

        var activeOwnerCount = await dbContext.UserRoles
            .Join(dbContext.Users, assignment => assignment.UserId, user => user.Id,
                (assignment, user) => new { assignment, user })
            .Where(item => item.assignment.RoleId == ownerRoleId.Value &&
                !item.user.IsDisabled &&
                (!item.user.LockoutEnd.HasValue || item.user.LockoutEnd <= now))
            .CountAsync(cancellationToken);
        return activeOwnerCount <= 1;
    }

    internal static async Task<bool> HasScimProvenanceAsync(
        AccountsDbContext dbContext,
        Guid userId,
        CancellationToken cancellationToken) =>
        await dbContext.ScimUserMappings.AnyAsync(mapping => mapping.UserId == userId, cancellationToken) ||
        await dbContext.FederatedIdentities.AnyAsync(identity => identity.UserId == userId, cancellationToken);

    internal static bool IsProvenanceDeletionConflict(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is Npgsql.PostgresException postgres &&
                postgres.SqlState == Npgsql.PostgresErrorCodes.ForeignKeyViolation &&
                (postgres.ConstraintName?.Contains("scim", StringComparison.OrdinalIgnoreCase) == true ||
                 postgres.ConstraintName?.Contains("federated", StringComparison.OrdinalIgnoreCase) == true))
            {
                return true;
            }
        }

        return false;
    }

    internal static bool IsExpectedConflict(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is Npgsql.PostgresException postgres &&
                postgres.SqlState is Npgsql.PostgresErrorCodes.SerializationFailure or
                    Npgsql.PostgresErrorCodes.DeadlockDetected or
                    Npgsql.PostgresErrorCodes.UniqueViolation)
            {
                return true;
            }
        }

        return false;
    }
}

internal static class AuthRoleOrdering
{
    internal static string ManagedRole(IEnumerable<string?> roles) =>
        roles.Any(role => string.Equals(role, AuthRoles.Owner, StringComparison.Ordinal))
            ? AuthRoles.Owner
            : roles.Any(role => string.Equals(role, AuthRoles.User, StringComparison.Ordinal))
                ? AuthRoles.User
                : AuthRoles.User;

    internal static string[] Ordered(IEnumerable<string?> roles) => roles
        .Where(role => !string.IsNullOrWhiteSpace(role))
        .Select(role => role!)
        .Distinct(StringComparer.Ordinal)
        .OrderBy(RoleOrder)
        .ThenBy(role => role, StringComparer.Ordinal)
        .ToArray();

    private static int RoleOrder(string role) => role switch
    {
        AuthRoles.Owner => 0,
        AuthRoles.User => 1,
        _ => 2,
    };
}