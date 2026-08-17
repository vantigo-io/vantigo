using System.Net;
using System.Text.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentityTenantCapabilitiesIntegrationTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task AnonymousUserCannotReadTenantCapabilities()
    {
        using var client = factory.CreateClient(new() { AllowAutoRedirect = false });
        var response = await client.GetAsync("/api/v1/identity/tenants/current/capabilities");
        Assert.True(response.StatusCode is HttpStatusCode.Unauthorized or HttpStatusCode.Redirect,
            $"expected unauthorized, got {response.StatusCode}");
    }

    [Fact]
    public async Task MemberReceivesModuleFlagsForActiveTenant()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var membership = await db.TenantMemberships.AsNoTracking()
                .FirstAsync(item => item.UserId == credentials.Id);
            var tenant = await db.Tenants.FirstAsync(item => item.Id == membership.TenantId);
            tenant.EnabledModules = ["customers", "products"];
            await db.SaveChangesAsync();
        }

        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        var response = await client.GetAsync("/api/v1/identity/tenants/current/capabilities");
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        using var payload = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
        var modules = payload.RootElement.GetProperty("modules").EnumerateArray()
            .ToDictionary(
                module => module.GetProperty("key").GetString()!,
                module => module.GetProperty("enabled").GetBoolean());

        Assert.Equal(new[] { "communications", "customers", "energy", "products" }, modules.Keys.Order().ToArray());
        Assert.True(modules["customers"]);
        Assert.True(modules["products"]);
        Assert.False(modules["communications"]);
        Assert.False(modules["energy"]);
        Assert.All(payload.RootElement.GetProperty("modules").EnumerateArray(),
            module => Assert.Equal(JsonValueKind.Object, module.GetProperty("config").ValueKind));
    }
}

[Collection(MultiTenantIdentityApiCollection.Name)]
public sealed class MultiTenantTenantCapabilitiesIntegrationTests(MultiTenantIdentityApiFactory factory)
{
    [Fact]
    public async Task UserWithoutTenantMembershipGetsNotFound()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        var response = await client.GetAsync("/api/v1/identity/tenants/current/capabilities");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
        using var payload = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
        Assert.Equal("no_active_tenant", payload.RootElement.GetProperty("code").GetString());
    }
}