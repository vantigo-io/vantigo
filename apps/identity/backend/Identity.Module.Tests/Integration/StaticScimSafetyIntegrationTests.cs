using System.Net;
using System.Net.Http.Headers;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

[Collection(StaticScimApiCollection.Name)]
public sealed class StaticScimSafetyIntegrationTests(StaticScimIdentityApiFactory factory) : IAsyncLifetime
{
    public async Task InitializeAsync() => await factory.ResetIdentityStateAsync();
    public Task DisposeAsync() => Task.CompletedTask;

    [Fact]
    public async Task StaticStateUsesDeterministicConnection()
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var connection = await db.ScimConnections.SingleAsync();
        Assert.Equal(ScimConnection.StaticId, connection.Id);
    }

    [Fact]
    public async Task StaticProtocolRejectsRemovedControlPlaneRoutes()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        Assert.Equal(HttpStatusCode.NotFound, (await owner.GetAsync("/api/v1/identity/access/scim")).StatusCode);
        using var scim = factory.CreateClient();
        scim.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", IdentityApiFactory.StaticScimToken);
        Assert.Equal(HttpStatusCode.OK, (await scim.GetAsync(ScimProtocolService.BasePath + "/ServiceProviderConfig")).StatusCode);
    }
}