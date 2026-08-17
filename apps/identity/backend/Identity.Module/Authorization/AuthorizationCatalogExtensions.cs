using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Authorization;

public static class AuthorizationCatalogExtensions
{
    /// <summary>Idempotently inserts protected built-in role templates for the current catalog.</summary>
    public static async Task EnsureBuiltInRolesAsync(
        this AccountsDbContext dbContext,
        RoleManager<IdentityRole<Guid>> roleManager,
        IPermissionCatalog catalog,
        CancellationToken cancellationToken = default)
    {
        var templates = new[]
        {
            (AuthRoles.SystemAdmin, "System administrator", "Global tenant control-plane administration.", true, true),
            (AuthRoles.Owner, "Owner", "Full installation access.", true, true),
            (AuthRoles.User, "User", "Standard user role with no permissions by default.", true, true),
        };
        foreach (var template in templates)
        {
            // Older bootstrap attempts could have inserted a role with a null
            // normalized name. Prefer the code-owned name before asking Identity
            // to look it up by normalized key, so initialization is idempotent.
            var role = await dbContext.Roles
                .OrderBy(item => item.Id)
                .FirstOrDefaultAsync(item => item.Name == template.Item1, cancellationToken)
                ?? await roleManager.FindByNameAsync(template.Item1);
            if (role is null)
            {
                role = new Microsoft.AspNetCore.Identity.IdentityRole<Guid>(template.Item1);
                role.NormalizedName = roleManager.NormalizeKey(template.Item1);
                var result = await roleManager.CreateAsync(role);
                if (!result.Succeeded)
                    throw new InvalidOperationException($"Unable to create protected role '{template.Item1}': " +
                        string.Join("; ", result.Errors.Select(error => error.Description)));
            }

            var normalizedName = roleManager.NormalizeKey(template.Item1);
            if (!string.Equals(role.NormalizedName, normalizedName, StringComparison.Ordinal))
            {
                role.NormalizedName = normalizedName;
                var result = await roleManager.UpdateAsync(role);
                if (!result.Succeeded)
                    throw new InvalidOperationException($"Unable to normalize protected role '{template.Item1}'.");
            }

            if (!await dbContext.RoleMetadata.AnyAsync(item => item.RoleId == role.Id, cancellationToken))
            {
                dbContext.RoleMetadata.Add(new RoleMetadata
                {
                    RoleId = role.Id,
                    DisplayName = template.Item2,
                    Description = template.Item3,
                    IsSystem = template.Item4,
                    IsBuiltIn = template.Item5,
                    ConcurrencyStamp = Guid.NewGuid().ToString("N"),
                });
            }
        }

        await dbContext.SaveChangesAsync(cancellationToken);
    }
}