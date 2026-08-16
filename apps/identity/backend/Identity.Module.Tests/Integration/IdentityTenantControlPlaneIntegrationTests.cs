using System.Net;
using System.Net.Http.Json;
using System.Text.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

[Collection(MultiTenantIdentityApiCollection.Name)]
public sealed class IdentityTenantControlPlaneIntegrationTests(MultiTenantIdentityApiFactory factory)
{
    [Fact]
    public async Task NonSystemAdminCannotUseTenantControlPlane()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var user = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        Assert.Equal(HttpStatusCode.Forbidden,
            (await user.GetAsync("/api/v1/identity/admin/tenants")).StatusCode);
    }

    [Fact]
    public async Task SystemAdminCanCreateUpdateSuspendAndReactivateTenant()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var slug = $"tenant-{Guid.NewGuid():N}";
        var create = await owner.PostAsJsonAsync("/api/v1/identity/admin/tenants", new
        {
            name = "Integration Tenant",
            slug,
            enabledModules = new[] { "Customers", "products" },
        });

        Assert.True(create.StatusCode == HttpStatusCode.Created,
            $"{create.StatusCode}: {await create.Content.ReadAsStringAsync()}");
        using var created = JsonDocument.Parse(await create.Content.ReadAsStringAsync());
        var id = created.RootElement.GetProperty("id").GetGuid();
        Assert.Equal(new[] { "customers", "products" },
            created.RootElement.GetProperty("enabledModules").EnumerateArray().Select(item => item.GetString()).ToArray());
        Assert.True(created.RootElement.GetProperty("membershipsCount").GetInt32() >= 1);

        var update = await owner.PutAsJsonAsync($"/api/v1/identity/admin/tenants/{id}", new
        {
            name = "Updated Tenant",
            slug,
            status = "Active",
            enabledModules = new[] { "energy" },
        });
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        Assert.Contains("Updated Tenant", await update.Content.ReadAsStringAsync());

        Assert.Equal(HttpStatusCode.OK,
            (await owner.PostAsync($"/api/v1/identity/admin/tenants/{id}/suspend", null)).StatusCode);
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var directory = scope.ServiceProvider.GetRequiredService<TenantDirectory>();
            Assert.Null(await directory.FindActiveBySlugAsync(slug));
            Assert.DoesNotContain(id, (await directory.GetActiveTenantsAsync()).Select(tenant => tenant.Value));
        }

        Assert.Equal(HttpStatusCode.OK,
            (await owner.PostAsync($"/api/v1/identity/admin/tenants/{id}/reactivate", null)).StatusCode);
        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDirectory = verifyScope.ServiceProvider.GetRequiredService<TenantDirectory>();
        Assert.Equal(id, (await verifyDirectory.FindActiveBySlugAsync(slug))!.Value.Value);
    }

    [Fact]
    public async Task SsoConfigIsGloballyUniqueAndDomainIsValidated()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var first = await CreateTenant(owner, "SSO One");
        var second = await CreateTenant(owner, "SSO Two");
        var entraId = Guid.NewGuid();

        var invalid = await owner.PutAsJsonAsync($"/api/v1/identity/admin/tenants/{first}/sso", new
        {
            entraTenantId = entraId,
            allowedEmailDomain = "not a domain",
            jitProvisioningEnabled = true,
        });
        Assert.Equal(HttpStatusCode.BadRequest, invalid.StatusCode);

        var configured = await owner.PutAsJsonAsync($"/api/v1/identity/admin/tenants/{first}/sso", new
        {
            entraTenantId = entraId,
            allowedEmailDomain = "integration.test",
            jitProvisioningEnabled = true,
        });
        Assert.Equal(HttpStatusCode.OK, configured.StatusCode);

        var duplicate = await owner.PutAsJsonAsync($"/api/v1/identity/admin/tenants/{second}/sso", new
        {
            entraTenantId = entraId,
            allowedEmailDomain = "other.test",
            jitProvisioningEnabled = false,
        });
        Assert.Equal(HttpStatusCode.Conflict, duplicate.StatusCode);
    }

    [Fact]
    public async Task InvalidModuleDoesNotProvisionTenantOrInvitation()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var slug = $"invalid-{Guid.NewGuid():N}";
        var email = $"admin-{Guid.NewGuid():N}@integration.test";
        var response = await owner.PostAsJsonAsync("/api/v1/identity/admin/tenants", new
        {
            name = "Invalid Tenant",
            slug,
            enabledModules = new[] { "not-a-module" },
            adminEmail = email,
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.False(await db.Tenants.AnyAsync(tenant => tenant.Slug == slug));
        Assert.False(await db.Invitations.AnyAsync(invitation => invitation.Email == email));
    }

    private static async Task<Guid> CreateTenant(HttpClient owner, string name)
    {
        var slug = $"{name.ToLowerInvariant().Replace(' ', '-')}-{Guid.NewGuid():N}";
        var response = await owner.PostAsJsonAsync("/api/v1/identity/admin/tenants", new
        {
            name,
            slug,
            enabledModules = new[] { "customers" },
        });
        Assert.True(response.StatusCode == HttpStatusCode.Created,
            $"{response.StatusCode}: {await response.Content.ReadAsStringAsync()}");
        using var document = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
        return document.RootElement.GetProperty("id").GetGuid();
    }
}