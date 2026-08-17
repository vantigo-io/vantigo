using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Identity;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Host;

internal static class IdentityDevelopmentSeeder
{
    private static readonly Guid DevelopmentOwnerId = new("7f4d1e5b-8a62-4b7e-9c13-2d5f6a708194");
    private static readonly Guid DevelopmentOwnerRoleId = new("8e5c2f6c-9b73-4c8f-ad24-3e607b8192a5");
    private static readonly Guid DevelopmentUserRoleId = new("9f6d307d-ac84-4d90-be35-4f718c92a3b6");
    private static readonly DateTimeOffset DevelopmentBootstrapCompletedAt =
        new(2026, 8, 4, 0, 0, 0, TimeSpan.Zero);

    internal static async Task SeedAsync(IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var adminConfiguration = scope.ServiceProvider.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value.Admin;
        var email = adminConfiguration.Email?.Trim();
        var displayName = adminConfiguration.DisplayName?.Trim();
        var password = adminConfiguration.Password;
        if (string.IsNullOrWhiteSpace(email) || string.IsNullOrWhiteSpace(displayName) || string.IsNullOrWhiteSpace(password))
        {
            throw new InvalidOperationException(
                "Development:Seed:Admin requires Email, DisplayName, and Password in Development configuration.");
        }

        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        var roleManager = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>();
        var userManager = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        await dbContext.EnsureBuiltInRolesAsync(
            roleManager,
            scope.ServiceProvider.GetRequiredService<IPermissionCatalog>(), cancellationToken);

        var user = await userManager.FindByEmailAsync(email);
        if (user is null)
        {
            user = new ApplicationUser
            {
                Id = DevelopmentOwnerId,
                UserName = email,
                Email = email,
                EmailConfirmed = true,
                DisplayName = displayName,
            };
            EnsureIdentitySuccess(await userManager.CreateAsync(user, password), "The development Owner account could not be created.");
        }

        await EnsureUserRoleAsync(userManager, user, AuthRoles.Owner);
        await EnsureUserRoleAsync(userManager, user, AuthRoles.SystemAdmin);
        await EnsureUserRoleAsync(userManager, user, AuthRoles.User);
        await EnsureDefaultTenantMembershipAsync(dbContext, user.Id, cancellationToken);
        if (!await dbContext.BootstrapStates.AnyAsync(state => state.Id == 1, cancellationToken))
        {
            dbContext.BootstrapStates.Add(new BootstrapState { Id = 1, CompletedAt = DevelopmentBootstrapCompletedAt });
            await dbContext.SaveChangesAsync(cancellationToken);
        }
        await transaction.CommitAsync(cancellationToken);
    }

    private static async Task EnsureRoleAsync(RoleManager<IdentityRole<Guid>> roleManager, string roleName, CancellationToken cancellationToken)
    {
        if (await roleManager.FindByNameAsync(roleName) is not null) return;
        var roleId = roleName == AuthRoles.Owner ? DevelopmentOwnerRoleId : DevelopmentUserRoleId;
        EnsureIdentitySuccess(await roleManager.CreateAsync(new IdentityRole<Guid>
        {
            Id = roleId,
            Name = roleName,
            NormalizedName = roleName.ToUpperInvariant(),
        }), $"The development role '{roleName}' could not be created.");
    }

    private static async Task EnsureDefaultTenantMembershipAsync(
        AccountsDbContext dbContext,
        Guid userId,
        CancellationToken cancellationToken)
    {
        var tenant = await dbContext.Tenants.SingleOrDefaultAsync(item => item.Slug == TenantSlug.Default, cancellationToken);
        if (tenant is null)
        {
            tenant = new Tenant
            {
                Name = "Default",
                Slug = TenantSlug.Default,
                Status = TenantStatus.Active,
                EnabledModules = [.. TenantModuleCatalog.KnownModuleKeys.OrderBy(key => key, StringComparer.Ordinal)],
                CreatedAtUtc = DateTimeOffset.UtcNow,
            };
            dbContext.Tenants.Add(tenant);
        }
        else if (tenant.EnabledModules.Length == 0)
        {
            tenant.EnabledModules = [.. TenantModuleCatalog.KnownModuleKeys.OrderBy(key => key, StringComparer.Ordinal)];
        }

        if (!await dbContext.TenantMemberships.AnyAsync(
                membership => membership.UserId == userId && membership.TenantId == tenant.Id, cancellationToken))
        {
            dbContext.TenantMemberships.Add(new TenantMembership
            {
                UserId = userId,
                TenantId = tenant.Id,
                CreatedAtUtc = DateTimeOffset.UtcNow,
            });
        }

        await dbContext.SaveChangesAsync(cancellationToken);
    }

    private static async Task EnsureUserRoleAsync(UserManager<ApplicationUser> userManager, ApplicationUser user, string roleName)
    {
        if (!await userManager.IsInRoleAsync(user, roleName))
        {
            EnsureIdentitySuccess(await userManager.AddToRoleAsync(user, roleName), $"The development account could not be assigned the '{roleName}' role.");
        }
    }

    private static void EnsureIdentitySuccess(IdentityResult result, string message)
    {
        if (result.Succeeded) return;
        var errors = string.Join("; ", result.Errors.Select(error => $"{error.Code}: {error.Description}"));
        throw new InvalidOperationException($"{message} {errors}");
    }
}