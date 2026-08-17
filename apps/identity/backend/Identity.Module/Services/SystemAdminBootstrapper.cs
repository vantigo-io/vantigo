using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

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
    RoleManager<IdentityRole<Guid>> roleManager,
    UserManager<ApplicationUser> userManager,
    IPermissionCatalog permissionCatalog,
    IOptions<VantigoAuthenticationOptions> authenticationOptions)
{
    public async Task EnsureAsync(CancellationToken cancellationToken = default)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        await dbContext.EnsureBuiltInRolesAsync(roleManager, permissionCatalog, cancellationToken);

        var configuredEmail = authenticationOptions.Value.SystemAdmin.Email?.Trim();
        if (string.IsNullOrWhiteSpace(configuredEmail))
        {
            await transaction.CommitAsync(cancellationToken);
            return;
        }

        // The one-time local bootstrap endpoint may create the configured
        // break-glass account after application startup.
        var user = await userManager.FindByEmailAsync(configuredEmail);
        if (user is null)
        {
            if (await dbContext.Users.AnyAsync(cancellationToken))
            {
                throw new InvalidOperationException(
                    $"Authentication:SystemAdmin:Email '{configuredEmail}' does not identify an existing account.");
            }

            await transaction.CommitAsync(cancellationToken);
            return;
        }

        await EnsureUserRoleAsync(user, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
    }

    private async Task EnsureUserRoleAsync(ApplicationUser user, CancellationToken cancellationToken)
    {
        if (await userManager.IsInRoleAsync(user, AuthRoles.SystemAdmin)) return;
        var result = await userManager.AddToRoleAsync(user, AuthRoles.SystemAdmin);
        if (!result.Succeeded)
        {
            throw new InvalidOperationException(
                "The configured SystemAdmin account could not be assigned its role: " +
                string.Join("; ", result.Errors.Select(error => error.Description)));
        }
    }
}