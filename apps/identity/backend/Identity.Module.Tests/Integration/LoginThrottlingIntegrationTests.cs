using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Identity.Tests.Integration;

/// <summary>
/// The login endpoint throttles by account + client address on top of the
/// IP-only rate-limit backstop, so credential stuffing against one account is
/// stopped without punishing other accounts or other clients.
/// </summary>
[Collection(IdentityApiCollection.Name)]
public sealed class LoginThrottlingIntegrationTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task Repeated_failures_for_one_account_are_throttled_without_affecting_others()
    {
        var client = await factory.CreateAntiforgeryClientAsync();
        var email = $"stuffing-{Guid.NewGuid():N}@integration.test";

        for (var attempt = 0; attempt < 10; attempt++)
        {
            var response = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password = "wrong-password" });
            Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
        }

        var throttled = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password = "wrong-password" });
        Assert.Equal(HttpStatusCode.TooManyRequests, throttled.StatusCode);
        var body = await throttled.Content.ReadAsStringAsync();
        Assert.Contains("rate_limited", body, StringComparison.Ordinal);

        // A different account from the same client is judged on its own
        // credentials, not the throttled account's failures.
        var otherAccount = await client.PostAsJsonAsync("/api/v1/identity/login",
            new { email = $"other-{Guid.NewGuid():N}@integration.test", password = "wrong-password" });
        Assert.Equal(HttpStatusCode.Unauthorized, otherAccount.StatusCode);
    }

    [Fact]
    public async Task A_successful_login_clears_the_account_failure_window()
    {
        var (_, email, password) = await factory.CreateUserWithCredentialsAsync("User");
        var client = await factory.CreateAntiforgeryClientAsync();

        for (var attempt = 0; attempt < 4; attempt++)
        {
            var failed = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password = "wrong-password" });
            Assert.Equal(HttpStatusCode.Unauthorized, failed.StatusCode);
        }

        var success = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password });
        Assert.Equal(HttpStatusCode.OK, success.StatusCode);
        await IdentityApiFactory.RefreshAntiforgeryAsync(client);

        // The cleared window means more attempts are available again.
        var afterSuccess = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password = "wrong-password" });
        Assert.Equal(HttpStatusCode.Unauthorized, afterSuccess.StatusCode);
    }
}