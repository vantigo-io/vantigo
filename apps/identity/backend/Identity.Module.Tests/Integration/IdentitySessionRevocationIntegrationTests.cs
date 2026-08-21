using System.Net;
using System.Net.Http.Json;
using System.Text.Json;

using Vantigo.Contracts.Identity;

namespace Vantigo.Identity.Tests.Integration;

/// <summary>
/// Server-side session revocation: an administrator, or the account itself, can cut
/// off cookies that are already in circulation.
/// </summary>
[Collection(IdentityApiCollection.Name)]
public sealed class IdentitySessionRevocationIntegrationTests(IdentityApiFactory factory)
{
    private const string SessionPath = "/api/v1/identity/session";
    private const string SelfRevokePath = "/api/v1/identity/account/sessions/revoke";

    [Fact]
    public async Task SelfRevocationEndsEverySessionForTheAccount()
    {
        (Guid Id, string Email, string Password) credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using HttpClient first = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        using HttpClient second = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        Assert.Equal(HttpStatusCode.OK, (await second.GetAsync(SessionPath)).StatusCode);

        HttpResponseMessage revocation = await first.PostAsync(SelfRevokePath, null);

        Assert.Equal(HttpStatusCode.OK, revocation.StatusCode);
        JsonElement body = await revocation.Content.ReadFromJsonAsync<JsonElement>();
        Assert.Equal(credentials.Id, body.GetProperty("userId").GetGuid());
        Assert.True(body.GetProperty("revoked").GetBoolean());
        Assert.Equal(HttpStatusCode.Unauthorized, (await second.GetAsync(SessionPath)).StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized, (await first.GetAsync(SessionPath)).StatusCode);
    }

    [Fact]
    public async Task SelfRevocationRequiresAuthentication()
    {
        using HttpClient client = await factory.CreateAntiforgeryClientAsync();

        HttpResponseMessage response = await client.PostAsync(SelfRevokePath, null);

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

    [Fact]
    public async Task SystemAdminRevokesAnotherAccountsSessions()
    {
        (Guid Id, string Email, string Password) credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using HttpClient victim = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        using HttpClient administrator = await factory.CreateOwnerClientAsync();

        HttpResponseMessage revocation = await administrator.PostAsync(RevokePathFor(credentials.Id), null);

        Assert.Equal(HttpStatusCode.OK, revocation.StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized, (await victim.GetAsync(SessionPath)).StatusCode);
        // Revoking someone else's sessions must not disturb the administrator's own.
        Assert.Equal(HttpStatusCode.OK, (await administrator.GetAsync(SessionPath)).StatusCode);
    }

    [Fact]
    public async Task RevokedAccountCanSignInAgain()
    {
        (Guid Id, string Email, string Password) credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using HttpClient administrator = await factory.CreateOwnerClientAsync();
        Assert.Equal(HttpStatusCode.OK, (await administrator.PostAsync(RevokePathFor(credentials.Id), null)).StatusCode);

        using HttpClient client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync(SessionPath)).StatusCode);
    }

    [Fact]
    public async Task NonAdministratorCannotRevokeAnotherAccountsSessions()
    {
        (Guid Id, string Email, string Password) target = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        (Guid Id, string Email, string Password) caller = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using HttpClient client = await factory.CreateAuthenticatedClientAsync(caller.Email, caller.Password);

        HttpResponseMessage response = await client.PostAsync(RevokePathFor(target.Id), null);

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task RevokingAnUnknownAccountReturnsNotFound()
    {
        using HttpClient administrator = await factory.CreateOwnerClientAsync();

        HttpResponseMessage response = await administrator.PostAsync(RevokePathFor(Guid.NewGuid()), null);

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    /// <summary>
    /// A stamp rotation that the caller triggers itself must end the other sessions
    /// without locking the caller out: their cookie is reissued against the new
    /// stamp, so a stale cached stamp has to be re-read before anything is rejected.
    /// </summary>
    [Fact]
    public async Task RotatingTheStampEndsTheOtherSessionsButNotTheCallers()
    {
        (Guid Id, string Email, string Password) credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using HttpClient caller = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        using HttpClient other = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        Assert.Equal(HttpStatusCode.OK, (await other.GetAsync(SessionPath)).StatusCode);

        HttpResponseMessage update = await caller.PutAsJsonAsync("/api/v1/identity/account/profile", new
        {
            displayName = "Rotated Session User",
            preferredLanguage = (string?)null,
        });

        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized, (await other.GetAsync(SessionPath)).StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await caller.GetAsync(SessionPath)).StatusCode);
    }

    private static string RevokePathFor(Guid userId) =>
        $"/api/v1/identity/system/users/{userId:D}/sessions/revoke";
}