using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging.Abstractions;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Identity;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentitySystemAdminBootstrapperTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task MixedCaseConfiguredEmailUsesIdentityLookupNormalization()
    {
        await factory.ResetIdentityStateAsync();
        try
        {
            var email = $"BreakGlass-{Guid.NewGuid():N}@Integration.Test";
            await using var scope = factory.Services.CreateAsyncScope();
            var userManager = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var user = new ApplicationUser
            {
                UserName = email,
                Email = email,
                EmailConfirmed = true,
                DisplayName = "Break Glass",
            };
            Assert.True((await userManager.CreateAsync(user)).Succeeded);
            await using (var dbScope = factory.Services.CreateAsyncScope())
            {
                var db = dbScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
                var storedUser = await db.Users.SingleAsync(item => item.Id == user.Id);
                storedUser.NormalizedEmail = email.ToLowerInvariant();
                await db.SaveChangesAsync();
            }

            var bootstrapper = CreateBootstrapper(scope.ServiceProvider, email, new LowercaseLookupNormalizer());
            await bootstrapper.EnsureAsync();

            Assert.True(await userManager.IsInRoleAsync(user, AuthRoles.SystemAdmin));
        }
        finally
        {
            await factory.ResetIdentityStateAsync();
        }
    }

    [Fact]
    public async Task MissingConfiguredAccountOnBootstrappedInstallationFailsStartup()
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var bootstrapper = CreateBootstrapper(scope.ServiceProvider,
            $"missing-{Guid.NewGuid():N}@integration.test");

        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => bootstrapper.EnsureAsync());

        Assert.Contains("break-glass administrator is missing", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public async Task MissingConfiguredAccountOnUnconsumedInstallationIsAllowed()
    {
        await factory.ResetIdentityStateWithoutBootstrapAsync();
        try
        {
            await using var scope = factory.Services.CreateAsyncScope();
            var bootstrapper = CreateBootstrapper(scope.ServiceProvider,
                $"first-run-{Guid.NewGuid():N}@integration.test");

            await bootstrapper.EnsureAsync();

            Assert.Equal(0, await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Users.CountAsync());
        }
        finally
        {
            await factory.ResetIdentityStateWithoutBootstrapAsync();
            await factory.BootstrapOwnerAsync();
        }
    }

    [Fact]
    public async Task RepeatedRunsAreIdempotentAndUpdateStampsWhenGranting()
    {
        await factory.ResetIdentityStateAsync();
        try
        {
            var email = $"stamp-{Guid.NewGuid():N}@integration.test";
            await using var scope = factory.Services.CreateAsyncScope();
            var userManager = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var user = new ApplicationUser
            {
                UserName = email,
                Email = email,
                EmailConfirmed = true,
                DisplayName = "Stamp Test",
            };
            Assert.True((await userManager.CreateAsync(user)).Succeeded);
            var before = await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Users.AsNoTracking()
                .Where(item => item.Id == user.Id).Select(item => new { item.SecurityStamp, item.ConcurrencyStamp }).SingleAsync();

            var bootstrapper = CreateBootstrapper(scope.ServiceProvider, email);
            await bootstrapper.EnsureAsync();
            var afterGrant = await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Users.AsNoTracking()
                .Where(item => item.Id == user.Id).Select(item => new { item.SecurityStamp, item.ConcurrencyStamp }).SingleAsync();
            await bootstrapper.EnsureAsync();
            var afterRepeat = await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Users.AsNoTracking()
                .Where(item => item.Id == user.Id).Select(item => new { item.SecurityStamp, item.ConcurrencyStamp }).SingleAsync();

            Assert.NotEqual(before.SecurityStamp, afterGrant.SecurityStamp);
            Assert.NotEqual(before.ConcurrencyStamp, afterGrant.ConcurrencyStamp);
            Assert.Equal(afterGrant.SecurityStamp, afterRepeat.SecurityStamp);
            Assert.Equal(afterGrant.ConcurrencyStamp, afterRepeat.ConcurrencyStamp);
        }
        finally
        {
            await factory.ResetIdentityStateAsync();
        }
    }

    [Fact]
    public async Task ExistingUserCreatedSystemAdminRoleConflictsWithProtectedMetadata()
    {
        await factory.ResetIdentityStateWithoutBootstrapAsync();
        try
        {
            await using var scope = factory.Services.CreateAsyncScope();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var role = new IdentityRole<Guid>
            {
                Name = AuthRoles.SystemAdmin,
                NormalizedName = AuthRoles.SystemAdmin.ToUpperInvariant(),
            };
            db.Roles.Add(role);
            db.RoleMetadata.Add(new RoleMetadata
            {
                RoleId = role.Id,
                DisplayName = "User-created SystemAdmin",
                Description = "Not protected",
                IsSystem = false,
                IsBuiltIn = false,
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            });
            await db.SaveChangesAsync();

            var bootstrapper = CreateBootstrapper(scope.ServiceProvider, null);
            var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => bootstrapper.EnsureAsync());
            Assert.Contains("conflicting metadata", exception.Message, StringComparison.Ordinal);
        }
        finally
        {
            await factory.ResetIdentityStateWithoutBootstrapAsync();
            await factory.BootstrapOwnerAsync();
        }
    }

    private static SystemAdminBootstrapper CreateBootstrapper(
        IServiceProvider services,
        string? email,
        ILookupNormalizer? normalizer = null)
    {
        var options = Options.Create(new VantigoAuthenticationOptions
        {
            SystemAdmin = new SystemAdminAuthenticationOptions { Email = email },
        });
        return new SystemAdminBootstrapper(
            services.GetRequiredService<AccountsDbContext>(),
            services.GetRequiredService<IPermissionCatalog>(),
            options,
            normalizer ?? services.GetRequiredService<ILookupNormalizer>(),
            NullLogger<SystemAdminBootstrapper>.Instance);
    }

    private sealed class LowercaseLookupNormalizer : ILookupNormalizer
    {
        public string? NormalizeName(string? name) => name?.Trim().ToLowerInvariant();

        public string? NormalizeEmail(string? email) => email?.Trim().ToLowerInvariant();
    }
}