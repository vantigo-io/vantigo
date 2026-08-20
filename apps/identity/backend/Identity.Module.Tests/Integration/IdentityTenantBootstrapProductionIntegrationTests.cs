using System.Net;
using System.Text.Json;

using Vantigo.Contracts.Identity;

namespace Vantigo.Identity.Tests.Integration;

/// <summary>
/// Boots the host outside Development against a clean database - mirroring a fresh
/// production deploy - and verifies the tenant bootstrap fix: the default tenant's
/// capabilities must reflect the host's enabled modules, not an empty array. See
/// GitHub issue #12 ("Fresh production tenant has all modules disabled").
/// </summary>
[Collection(ProductionIdentityApiCollection.Name)]
public sealed class IdentityTenantBootstrapProductionIntegrationTests(ProductionIdentityApiFactory factory)
{
    [Fact]
    public async Task FreshProductionTenantReportsEveryHostEnabledModuleAsEnabled()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        var response = await client.GetAsync("/api/v1/identity/tenants/current/capabilities");
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        using var payload = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
        var modules = payload.RootElement.GetProperty("modules").EnumerateArray()
            .ToDictionary(
                module => module.GetProperty("key").GetString()!,
                module => module.GetProperty("enabled").GetBoolean());

        Assert.Equal(new[] { "communications", "customers", "energy", "products" }, modules.Keys.Order().ToArray());
        // ProductionIdentityApiFactory host-enables Customers, Products, and Energy.
        Assert.True(modules["customers"]);
        Assert.True(modules["products"]);
        Assert.True(modules["energy"]);
        Assert.False(modules["communications"]);
    }
}