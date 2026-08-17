using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Authorization;

public static class AuthorizationCatalogExtensions
{
    private const int MaxEnsureAttempts = 4;

    /// <summary>Idempotently inserts protected built-in roles without requiring Identity managers.</summary>
    public static Task EnsureBuiltInRolesAsync(
        this AccountsDbContext dbContext,
        IPermissionCatalog catalog,
        CancellationToken cancellationToken = default) =>
        EnsureWithRetryAsync(dbContext, catalog, null, cancellationToken);

    /// <summary>Idempotently inserts protected built-in role templates for the current catalog.</summary>
    public static Task EnsureBuiltInRolesAsync(
        this AccountsDbContext dbContext,
        RoleManager<IdentityRole<Guid>> roleManager,
        IPermissionCatalog catalog,
        CancellationToken cancellationToken = default) =>
        EnsureWithRetryAsync(dbContext, catalog, roleManager, cancellationToken);

    private static async Task EnsureWithRetryAsync(
        AccountsDbContext dbContext,
        IPermissionCatalog catalog,
        RoleManager<IdentityRole<Guid>>? roleManager,
        CancellationToken cancellationToken)
    {
        // The catalog is part of the method contract because callers ensure the
        // complete authorization model before materializing protected roles.
        _ = catalog;

        if (dbContext.Database.CurrentTransaction is not null)
        {
            await EnsureBuiltInRolesCoreAsync(dbContext, roleManager, cancellationToken);
            return;
        }

        for (var attempt = 1; ; attempt++)
        {
            try
            {
                await using var transaction = await dbContext.Database.BeginTransactionAsync(
                    System.Data.IsolationLevel.Serializable, cancellationToken);
                await EnsureBuiltInRolesCoreAsync(dbContext, roleManager, cancellationToken);
                await transaction.CommitAsync(cancellationToken);
                return;
            }
            catch (Exception exception) when (IsRetryableConflict(exception) && attempt < MaxEnsureAttempts)
            {
                dbContext.ChangeTracker.Clear();
                await Task.Delay(TimeSpan.FromMilliseconds(25 * attempt), cancellationToken);
            }
        }
    }

    private static async Task EnsureBuiltInRolesCoreAsync(
        AccountsDbContext dbContext,
        RoleManager<IdentityRole<Guid>>? roleManager,
        CancellationToken cancellationToken)
    {
        foreach (var template in BuiltInRoleTemplates)
        {
            var normalizedName = roleManager?.NormalizeKey(template.Name) ?? template.Name.ToUpperInvariant();
            if (string.IsNullOrWhiteSpace(normalizedName))
                throw new InvalidOperationException($"Unable to normalize protected role '{template.Name}'.");

            var roles = await dbContext.Roles
                .Where(item => item.Name != null || item.NormalizedName != null)
                .ToListAsync(cancellationToken);
            var candidates = roles.Where(item =>
                    string.Equals(item.Name, template.Name, StringComparison.OrdinalIgnoreCase) ||
                    string.Equals(item.NormalizedName, normalizedName, StringComparison.OrdinalIgnoreCase))
                .OrderBy(item => item.Id)
                .ToArray();

            if (candidates.Length > 1)
            {
                throw new InvalidOperationException(
                    $"Protected role '{template.Name}' has multiple colliding role rows. Remove the ambiguous role names before startup.");
            }

            var role = candidates.SingleOrDefault();
            if (role is null)
            {
                role = new IdentityRole<Guid>(template.Name)
                {
                    Id = Guid.NewGuid(),
                    NormalizedName = normalizedName,
                };
                dbContext.Roles.Add(role);
                // IdentityRole<Guid> does not assign its key until the entity is
                // persisted. Save it before creating the metadata row that uses
                // the generated role id as its primary/foreign key.
                await dbContext.SaveChangesAsync(cancellationToken);
            }
            else
            {
                var hasCanonicalName = string.Equals(role.Name, template.Name, StringComparison.Ordinal);
                var hasSemanticName = string.Equals(role.Name, template.Name, StringComparison.OrdinalIgnoreCase);
                var hasCanonicalNormalizedName = string.Equals(role.NormalizedName, normalizedName, StringComparison.Ordinal);
                if (!hasSemanticName && string.Equals(role.NormalizedName, normalizedName, StringComparison.OrdinalIgnoreCase))
                {
                    throw new InvalidOperationException(
                        $"Role '{role.Name}' conflicts with the protected role name '{template.Name}'.");
                }

                // Repair case-only names and null/stale normalized names before
                // metadata is trusted by the assignment APIs.
                if (!hasCanonicalName) role.Name = template.Name;
                if (!hasCanonicalNormalizedName) role.NormalizedName = normalizedName;
            }

            await EnsureRoleMetadataAsync(dbContext, role, template, cancellationToken);
        }

        await dbContext.SaveChangesAsync(cancellationToken);
    }

    private static readonly BuiltInRoleTemplate[] BuiltInRoleTemplates =
    [
        new(AuthRoles.SystemAdmin, "System administrator", "Global tenant control-plane administration."),
        new(AuthRoles.Owner, "Owner", "Full installation access."),
        new(AuthRoles.User, "User", "Standard user role with no permissions by default."),
    ];

    private static async Task EnsureRoleMetadataAsync(
        AccountsDbContext dbContext,
        IdentityRole<Guid> role,
        BuiltInRoleTemplate template,
        CancellationToken cancellationToken)
    {
        var metadata = await dbContext.RoleMetadata
            .SingleOrDefaultAsync(item => item.RoleId == role.Id, cancellationToken);
        if (metadata is null)
        {
            dbContext.RoleMetadata.Add(new RoleMetadata
            {
                RoleId = role.Id,
                DisplayName = template.DisplayName,
                Description = template.Description,
                IsSystem = true,
                IsBuiltIn = true,
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            });
            return;
        }

        if (!metadata.IsSystem || !metadata.IsBuiltIn)
        {
            throw new InvalidOperationException(
                $"Role '{template.Name}' already exists with conflicting metadata and cannot be adopted as a protected role.");
        }

        // A row unambiguously identified as a built-in role can be repaired to
        // the code-owned protected values. User-created metadata is rejected
        // above rather than silently gaining protected semantics.
        var changed = !string.Equals(metadata.DisplayName, template.DisplayName, StringComparison.Ordinal) ||
            !string.Equals(metadata.Description, template.Description, StringComparison.Ordinal) ||
            metadata.StewardUserId.HasValue;
        metadata.DisplayName = template.DisplayName;
        metadata.Description = template.Description;
        metadata.StewardUserId = null;
        if (changed) metadata.ConcurrencyStamp = Guid.NewGuid().ToString("N");
    }

    private static bool IsRetryableConflict(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is PostgresException postgres && postgres.SqlState is
                PostgresErrorCodes.UniqueViolation or
                PostgresErrorCodes.SerializationFailure or
                PostgresErrorCodes.DeadlockDetected)
                return true;
        }

        return false;
    }

    private sealed record BuiltInRoleTemplate(string Name, string DisplayName, string Description);
}