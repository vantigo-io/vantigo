using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class TenantBootstrapperTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task FreshDefaultTenantIsInitializedWithHostEnabledModules()
    {
        await factory.ResetIdentityStateWithoutBootstrapAsync();
        try
        {
            await using var scope = factory.Services.CreateAsyncScope();
            var bootstrapper = CreateBootstrapper(scope.ServiceProvider,
                isMultiTenant: false,
                customers: true, communications: false, products: true, energy: false);

            await bootstrapper.EnsureAsync();

            var tenant = await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Tenants
                .AsNoTracking().SingleAsync(item => item.Slug == TenantSlug.Default);
            Assert.Equal(["customers", "products"], tenant.EnabledModules);
        }
        finally
        {
            await factory.ResetIdentityStateWithoutBootstrapAsync();
            await factory.BootstrapOwnerAsync();
        }
    }

    [Fact]
    public async Task PreExistingEmptyModulesAreBackfilledFromHostConfiguration()
    {
        await factory.ResetIdentityStateWithoutBootstrapAsync();
        try
        {
            await using var scope = factory.Services.CreateAsyncScope();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            db.Tenants.Add(new Tenant
            {
                Name = "Default",
                Slug = TenantSlug.Default,
                Status = TenantStatus.Active,
                EnabledModules = [],
                CreatedAtUtc = DateTimeOffset.UtcNow,
            });
            await db.SaveChangesAsync();

            var bootstrapper = CreateBootstrapper(scope.ServiceProvider,
                isMultiTenant: false,
                customers: true, communications: true, products: true, energy: true);

            await bootstrapper.EnsureAsync();

            var tenant = await db.Tenants.AsNoTracking().SingleAsync(item => item.Slug == TenantSlug.Default);
            Assert.Equal(["communications", "customers", "energy", "products"], tenant.EnabledModules);
        }
        finally
        {
            await factory.ResetIdentityStateWithoutBootstrapAsync();
            await factory.BootstrapOwnerAsync();
        }
    }

    [Fact]
    public async Task NonEmptyModulesAreNeverOverridden()
    {
        await factory.ResetIdentityStateWithoutBootstrapAsync();
        try
        {
            await using var scope = factory.Services.CreateAsyncScope();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            db.Tenants.Add(new Tenant
            {
                Name = "Default",
                Slug = TenantSlug.Default,
                Status = TenantStatus.Active,
                EnabledModules = ["customers"],
                CreatedAtUtc = DateTimeOffset.UtcNow,
            });
            await db.SaveChangesAsync();

            // The host now enables every module, but the tenant's narrower, non-empty
            // selection reflects a prior explicit choice and must not be widened.
            var bootstrapper = CreateBootstrapper(scope.ServiceProvider,
                isMultiTenant: false,
                customers: true, communications: true, products: true, energy: true);

            await bootstrapper.EnsureAsync();

            var tenant = await db.Tenants.AsNoTracking().SingleAsync(item => item.Slug == TenantSlug.Default);
            Assert.Equal(["customers"], tenant.EnabledModules);
        }
        finally
        {
            await factory.ResetIdentityStateWithoutBootstrapAsync();
            await factory.BootstrapOwnerAsync();
        }
    }

    [Fact]
    public async Task RepeatedRunsAreIdempotent()
    {
        await factory.ResetIdentityStateWithoutBootstrapAsync();
        try
        {
            await using var scope = factory.Services.CreateAsyncScope();
            var bootstrapper = CreateBootstrapper(scope.ServiceProvider,
                isMultiTenant: false,
                customers: true, communications: false, products: false, energy: false);

            await bootstrapper.EnsureAsync();
            await bootstrapper.EnsureAsync();

            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var tenants = await db.Tenants.AsNoTracking().Where(item => item.Slug == TenantSlug.Default).ToListAsync();
            var tenant = Assert.Single(tenants);
            Assert.Equal(["customers"], tenant.EnabledModules);
        }
        finally
        {
            await factory.ResetIdentityStateWithoutBootstrapAsync();
            await factory.BootstrapOwnerAsync();
        }
    }

    private static TenantBootstrapper CreateBootstrapper(
        IServiceProvider services,
        bool isMultiTenant,
        bool customers,
        bool communications,
        bool products,
        bool energy)
    {
        var tenancyOptions = Options.Create(new TenancyOptions
        {
            Mode = isMultiTenant ? TenancyOptions.MultiMode : TenancyOptions.SingleMode,
        });
        var moduleHostingOptions = Options.Create(new ModuleHostingOptions
        {
            Customers = new ModuleToggleOptions { Enabled = customers },
            Communications = new ModuleToggleOptions { Enabled = communications },
            Products = new ModuleToggleOptions { Enabled = products },
            Energy = new ModuleToggleOptions { Enabled = energy },
        });
        return new TenantBootstrapper(
            services.GetRequiredService<AccountsDbContext>(),
            tenancyOptions,
            moduleHostingOptions);
    }
}