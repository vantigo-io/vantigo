using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Identity;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

/// <summary>
/// Materializes the configured global SystemAdmin assignment without exposing
/// that role through tenant or ordinary account-management APIs.
/// </summary>
public sealed class SystemAdminBootstrapper(
    AccountsDbContext dbContext,
    IPermissionCatalog permissionCatalog,
    IOptions<VantigoAuthenticationOptions> authenticationOptions,
    ILookupNormalizer lookupNormalizer,
    ILogger<SystemAdminBootstrapper> logger)
{
    private const int MaxEnsureAttempts = 4;

    public async Task EnsureAsync(CancellationToken cancellationToken = default)
    {
        await dbContext.EnsureBuiltInRolesAsync(permissionCatalog, cancellationToken);

        var configuredEmail = authenticationOptions.Value.SystemAdmin.Email?.Trim();
        if (string.IsNullOrWhiteSpace(configuredEmail))
            return;

        var normalizedEmail = lookupNormalizer.NormalizeEmail(configuredEmail);
        if (string.IsNullOrWhiteSpace(normalizedEmail))
            throw new InvalidOperationException("Authentication:SystemAdmin:Email could not be normalized.");

        for (var attempt = 1; ; attempt++)
        {
            try
            {
                await using var transaction = await dbContext.Database.BeginTransactionAsync(
                    System.Data.IsolationLevel.Serializable, cancellationToken);

                // The one-time local bootstrap endpoint may create the configured
                // break-glass account after application startup.
                var user = await dbContext.Users
                    .SingleOrDefaultAsync(item => item.NormalizedEmail == normalizedEmail, cancellationToken);
                if (user is null)
                {
                    var installationIsConsumed = await dbContext.Users.AnyAsync(cancellationToken) ||
                        await dbContext.BootstrapStates.AnyAsync(state => state.Id == 1, cancellationToken);
                    if (installationIsConsumed)
                    {
                        throw new InvalidOperationException(
                            $"Authentication:SystemAdmin:Email '{configuredEmail}' does not match an existing user. " +
                            "The configured break-glass administrator is missing from an already bootstrapped installation.");
                    }

                    logger.LogInformation(
                        "Configured SystemAdmin email {Email} does not match an account yet; allowing the unconsumed first-run bootstrap to create it.",
                        configuredEmail);
                    await transaction.CommitAsync(cancellationToken);
                    return;
                }

                var role = await dbContext.Roles.SingleAsync(item => item.Name == AuthRoles.SystemAdmin, cancellationToken);
                var hasRole = await dbContext.Set<ApplicationUserRole>().AnyAsync(
                    item => item.UserId == user.Id && item.RoleId == role.Id, cancellationToken);
                if (!hasRole)
                {
                    dbContext.Set<ApplicationUserRole>().Add(new ApplicationUserRole
                    {
                        UserId = user.Id,
                        RoleId = role.Id,
                    });
                    user.SecurityStamp = Guid.NewGuid().ToString("N");
                    user.ConcurrencyStamp = Guid.NewGuid().ToString("N");
                    await dbContext.SaveChangesAsync(cancellationToken);
                }

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
}