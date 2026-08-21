using System.Net;

using Vantigo.Contracts.Identity;

namespace Vantigo.Identity.Tests.Integration;

/// <summary>
/// Session bounds against a host configured with second-scale lifetimes. The
/// behaviour under test is the relationship between the bounds, not the shipped
/// defaults, which are hours long and covered by the configuration tests.
/// </summary>
[Collection(ShortSessionIdentityApiCollection.Name)]
public sealed class IdentitySessionLifetimeIntegrationTests(ShortSessionIdentityApiFactory factory)
{
    private static readonly TimeSpan PollInterval = TimeSpan.FromMilliseconds(500);
    private const string SessionPath = "/api/v1/identity/session";

    [Fact]
    public async Task PrivilegedSessionEndsAfterTheShorterIdleWindow()
    {
        using HttpClient client = await factory.CreateOwnerClientAsync();
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync(SessionPath)).StatusCode);

        await Task.Delay(ShortSessionIdentityApiFactory.PrivilegedIdleTimeout + TimeSpan.FromMilliseconds(1000));

        Assert.Equal(HttpStatusCode.Unauthorized, (await client.GetAsync(SessionPath)).StatusCode);
    }

    [Fact]
    public async Task StandardSessionSurvivesThePrivilegedIdleWindow()
    {
        (Guid Id, string Email, string Password) credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using HttpClient client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        await Task.Delay(ShortSessionIdentityApiFactory.PrivilegedIdleTimeout + TimeSpan.FromMilliseconds(1000));

        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync(SessionPath)).StatusCode);
    }

    [Fact]
    public async Task ActivityRenewsAPrivilegedSessionInsideTheIdleWindow()
    {
        using HttpClient client = await factory.CreateOwnerClientAsync();

        // Requests spanning longer than the idle window. Each one has to renew the
        // window, otherwise the session dies partway through this loop.
        for (int attempt = 0; attempt < 5; attempt++)
        {
            Assert.Equal(HttpStatusCode.OK, (await client.GetAsync(SessionPath)).StatusCode);
            await Task.Delay(TimeSpan.FromSeconds(1));
        }

        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync(SessionPath)).StatusCode);
    }

    [Fact]
    public async Task ContinuouslyUsedSessionStillEndsAtTheAbsoluteLifetime()
    {
        (Guid Id, string Email, string Password) credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using HttpClient client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        // The idle window is hours long for a standard account here, so nothing but
        // the absolute lifetime can end a session that never stops making requests.
        DateTimeOffset giveUpAt = DateTimeOffset.UtcNow + ShortSessionIdentityApiFactory.AbsoluteLifetime + TimeSpan.FromSeconds(4);
        HttpStatusCode statusCode;
        do
        {
            statusCode = (await client.GetAsync(SessionPath)).StatusCode;
            if (statusCode == HttpStatusCode.Unauthorized) break;
            Assert.Equal(HttpStatusCode.OK, statusCode);
            await Task.Delay(PollInterval);
        }
        while (DateTimeOffset.UtcNow < giveUpAt);

        Assert.Equal(HttpStatusCode.Unauthorized, statusCode);
    }
}