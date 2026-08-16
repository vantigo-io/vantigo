using System.Net;
using System.Net.Http.Json;

using Vantigo.Contracts.Identity;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentityControlPlaneIntegrationTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task LocalAccessGroupAdministrationRemainsOwnerOnly()
    {
        using var anonymous = factory.CreateCookieClient();
        Assert.Equal(HttpStatusCode.Unauthorized, (await anonymous.GetAsync("/api/v1/identity/access/groups")).StatusCode);
        using var owner = await factory.CreateOwnerClientAsync();
        var response = await owner.PostAsJsonAsync("/api/v1/identity/access/groups", new { displayName = "Local integration group", isActive = true });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        Assert.Contains("Local integration group", await owner.GetStringAsync("/api/v1/identity/access/groups"));
    }

    [Fact]
    public async Task TenantControlPlane_IsGatedToMultiTenantMode()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var response = await owner.GetAsync("/api/v1/identity/admin/tenants");

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
        Assert.Contains("multi_tenant_required", await response.Content.ReadAsStringAsync());
    }
}