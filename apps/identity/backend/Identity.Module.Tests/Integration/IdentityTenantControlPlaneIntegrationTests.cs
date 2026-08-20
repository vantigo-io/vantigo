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
    public async Task OwnerWithoutSystemAdminCannotUseAnyTenantControlPlaneEndpoint()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.Owner);
        await using (var verificationScope = factory.Services.CreateAsyncScope())
        {
            var verificationDb = verificationScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var assignedRoles = await verificationDb.UserRoles.Where(item => item.UserId == credentials.Id)
                .Join(verificationDb.Roles, item => item.RoleId, role => role.Id, (_, role) => role.Name).ToArrayAsync();
            Assert.Single(assignedRoles);
            Assert.Equal(AuthRoles.Owner, assignedRoles[0]);
        }
        using var owner = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        var id = Guid.NewGuid();
        var requests = new Func<Task<HttpResponseMessage>>[]
        {
            () => owner.GetAsync("/api/v1/identity/admin/tenants"),
            () => owner.GetAsync($"/api/v1/identity/admin/tenants/{id}"),
            () => owner.PostAsJsonAsync("/api/v1/identity/admin/tenants", new { name = "Denied", slug = $"denied-{Guid.NewGuid():N}", enabledModules = new[] { "customers" } }),
            () => owner.PutAsJsonAsync($"/api/v1/identity/admin/tenants/{id}", new { name = "Denied" }),
            () => owner.PostAsync($"/api/v1/identity/admin/tenants/{id}/suspend", null),
            () => owner.PostAsync($"/api/v1/identity/admin/tenants/{id}/reactivate", null),
        };

        for (var index = 0; index < requests.Length; index++)
        {
            var response = await requests[index]();
            Assert.True(response.StatusCode == HttpStatusCode.Forbidden,
                $"request {index} was {response.StatusCode}: {await response.Content.ReadAsStringAsync()}");
        }
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
    public async Task SessionExposesSystemAdminFlag()
    {
        using var systemAdmin = await factory.CreateOwnerClientAsync();
        using var systemAdminSession = JsonDocument.Parse(
            await (await systemAdmin.GetAsync("/api/v1/identity/session")).Content.ReadAsStringAsync());
        Assert.True(systemAdminSession.RootElement.GetProperty("isSystemAdmin").GetBoolean());

        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var user = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        using var userSession = JsonDocument.Parse(
            await (await user.GetAsync("/api/v1/identity/session")).Content.ReadAsStringAsync());
        Assert.False(userSession.RootElement.GetProperty("isSystemAdmin").GetBoolean());
    }

    [Fact]
    public async Task RemovedTenantSsoAndOffboardingEndpointsAreNotMapped()
    {
        using var systemAdmin = await factory.CreateOwnerClientAsync();
        var tenantId = await CreateTenant(systemAdmin, "Removed Control Plane");
        var requests = new Func<Task<HttpResponseMessage>>[]
        {
            () => systemAdmin.GetAsync($"/api/v1/identity/admin/tenants/{tenantId}/sso"),
            () => systemAdmin.PutAsJsonAsync($"/api/v1/identity/admin/tenants/{tenantId}/sso",
                new { entraTenantId = Guid.NewGuid(), allowedEmailDomain = "integration.test", jitProvisioningEnabled = true }),
            () => systemAdmin.GetAsync($"/api/v1/identity/admin/tenants/{tenantId}/offboarding"),
            () => systemAdmin.PostAsync($"/api/v1/identity/admin/tenants/{tenantId}/offboarding/export", null),
            () => systemAdmin.PostAsJsonAsync($"/api/v1/identity/admin/tenants/{tenantId}/offboarding/purge", new { }),
        };

        for (var index = 0; index < requests.Length; index++)
        {
            var response = await requests[index]();
            Assert.True(response.StatusCode == HttpStatusCode.NotFound,
                $"request {index} was {response.StatusCode}: {await response.Content.ReadAsStringAsync()}");
        }

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var tables = await db.Database.SqlQuery<string>(
            $"select table_name from information_schema.tables where table_schema = 'identity'").ToListAsync();
        Assert.DoesNotContain("tenant_sso_configurations", tables);
        Assert.DoesNotContain("tenant_offboarding_states", tables);
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