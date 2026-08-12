using System.Net;
using System.Net.Http.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityMfaApiCollection.Name)]
public sealed class IdentityMfaIntegrationTests(MfaIdentityApiFactory factory)
{
    [Fact]
    public async Task OwnerManagement_RequiresMfaWhenConfigured()
    {
        using var owner = await factory.CreateOwnerClientAsync();

        var response = await owner.GetAsync("/api/v1/identity/owner/users");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task AuthorizationManagement_RequiresMfaWhenConfigured()
    {
        using var owner = await factory.CreateOwnerClientAsync();

        var response = await owner.GetAsync("/api/v1/identity/access/roles");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task OrdinaryAuthenticatedUserCanEnrollMfaAndCrossUserResetRemainsDenied()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync("MfaDelegate");
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        var setup = await client.PostAsync("/api/v1/identity/owner/mfa/setup", null);
        Assert.Equal(HttpStatusCode.OK, setup.StatusCode);
        var setupBody = await setup.Content.ReadFromJsonAsync<SetupResponse>();
        Assert.NotNull(setupBody);
        var code = IdentityApiFactory.CreateTotpCode(setupBody!.SharedKey!);
        var enable = await client.PostAsJsonAsync("/api/v1/identity/owner/mfa/enable", new { code });
        Assert.Equal(HttpStatusCode.OK, enable.StatusCode);

        await using var scope = factory.Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var owner = await users.FindByEmailAsync(IdentityApiFactory.OwnerEmail);
        Assert.NotNull(owner);
        var reset = await client.PostAsync($"/api/v1/identity/owner/mfa/reset/{owner!.Id}", null);
        Assert.Equal(HttpStatusCode.Forbidden, reset.StatusCode);
    }

    private sealed record SetupResponse(string? SharedKey, string? AuthenticatorUri, bool Initialized);
}