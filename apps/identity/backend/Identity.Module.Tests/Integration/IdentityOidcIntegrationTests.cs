using System.Net;
using System.Net.Http.Json;
using System.Text.Json;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authentication.OpenIdConnect;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityOidcApiCollection.Name)]
public sealed class IdentityOidcIntegrationTests(OidcIdentityApiFactory factory)
{
    [Fact]
    public async Task OidcCodePkceFlow_UsesRealMiddlewareAndJitProvisionsUser()
    {
        using var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });

        var providers = await client.GetFromJsonAsync<JsonElement>("/api/v1/identity/providers");
        Assert.Equal("Workforce SSO", providers.GetProperty("oidc").GetProperty("displayName").GetString());
        var scheme = await factory.Services.GetRequiredService<IAuthenticationSchemeProvider>()
            .GetSchemeAsync(WorkforceOidcOptions.Scheme);
        Assert.NotNull(scheme);
        var oidcOptions = factory.Services.GetRequiredService<IOptionsMonitor<OpenIdConnectOptions>>()
            .Get(WorkforceOidcOptions.Scheme);
        Assert.NotNull(oidcOptions.Backchannel);
        Assert.Equal(IdentityApiFactory.OidcAuthority, oidcOptions.Authority);
        Assert.Equal(IdentityApiFactory.OidcClientId, oidcOptions.ClientId);
        Assert.Equal(WorkforceOidcOptions.DefaultCallbackPath, oidcOptions.CallbackPath);

        var challenge = await client.GetAsync("/api/v1/identity/oidc/challenge");
        Assert.True(challenge.StatusCode == HttpStatusCode.Redirect,
            $"Expected redirect, got {challenge.StatusCode}: {await challenge.Content.ReadAsStringAsync()}");
        var authorization = ParseAuthorization(challenge.Headers.Location!);
        Assert.Equal("code", authorization["response_type"]);
        Assert.Equal(IdentityApiFactory.OidcClientId, authorization["client_id"]);
        var redirectUri = new Uri(authorization["redirect_uri"]);
        Assert.Equal(WorkforceOidcOptions.DefaultCallbackPath, redirectUri.AbsolutePath);
        Assert.False(string.IsNullOrWhiteSpace(authorization["state"]));
        Assert.False(string.IsNullOrWhiteSpace(authorization["nonce"]));
        Assert.Equal("S256", authorization["code_challenge_method"]);
        Assert.False(string.IsNullOrWhiteSpace(authorization["code_challenge"]));
        Assert.Equal("openid profile email", authorization["scope"]);
        Assert.DoesNotContain("client_secret", challenge.Headers.Location!.Query, StringComparison.OrdinalIgnoreCase);

        var subject = $"oidc-code-{Guid.NewGuid():N}";
        var callback = await client.GetAsync(
            $"{WorkforceOidcOptions.DefaultCallbackPath}?code={Uri.EscapeDataString($"oidc-test:{subject}:{authorization["nonce"]}:{authorization["code_challenge"]}")}" +
            $"&state={Uri.EscapeDataString(authorization["state"])}");
        Assert.Equal(HttpStatusCode.Redirect, callback.StatusCode);
        Assert.Equal(WorkforceOidcOptions.CompletionPath, callback.Headers.Location?.OriginalString);

        var completion = await client.GetAsync(callback.Headers.Location);
        Assert.Equal(HttpStatusCode.Redirect, completion.StatusCode);
        Assert.Equal("/", completion.Headers.Location?.OriginalString);

        var session = await client.GetFromJsonAsync<JsonElement>("/api/v1/identity/session");
        Assert.Equal($"{subject}@integration.test", session.GetProperty("user").GetProperty("email").GetString());
        Assert.Equal(new[] { "User" }, session.GetProperty("user").GetProperty("roles").EnumerateArray()
            .Select(item => item.GetString()).ToArray());

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.Equal(1, await db.Users.CountAsync(user => user.Email == $"{subject}@integration.test"));
        Assert.Equal(1, await db.UserLogins.CountAsync(login => login.LoginProvider == IdentityApiFactory.OidcAuthority && login.ProviderKey == subject));
        var oidcUserId = await db.Users
            .Where(user => user.Email == $"{subject}@integration.test")
            .Select(user => user.Id)
            .SingleAsync();

        using var owner = await factory.CreateOwnerClientAsync();
        var users = await owner.GetFromJsonAsync<JsonElement[]>("/api/v1/identity/owner/users");
        var listedUser = Assert.Single(users!, user => user.GetProperty("id").GetGuid() == oidcUserId);
        Assert.True(listedUser.GetProperty("ssoEnabled").GetBoolean());
    }

    [Fact]
    public async Task OidcCodeRedemptionFailure_IsRedirectedWithoutCreatingSessionOrUser()
    {
        using var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });

        var challenge = await client.GetAsync("/api/v1/identity/oidc/challenge");
        var authorization = ParseAuthorization(challenge.Headers.Location!);
        var callback = await client.GetAsync(
            $"{WorkforceOidcOptions.DefaultCallbackPath}?code=invalid_grant:{Uri.EscapeDataString($"failure-{Guid.NewGuid():N}")}" +
            $"&state={Uri.EscapeDataString(authorization["state"])}");

        Assert.Equal(HttpStatusCode.Redirect, callback.StatusCode);
        Assert.Equal("/sign-in?error=oidc_authentication_failed", callback.Headers.Location?.OriginalString);
        Assert.Equal(HttpStatusCode.Unauthorized, (await client.GetAsync("/api/v1/identity/session")).StatusCode);

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.DoesNotContain(await db.Users.Select(user => user.Email).ToListAsync(),
            email => email?.StartsWith("failure-", StringComparison.Ordinal) == true);
    }

    [Fact]
    public async Task DisabledExistingOidcAccount_IsRejectedWithGenericAccountLockedError()
    {
        var subject = $"disabled-oidc-{Guid.NewGuid():N}";
        var email = $"disabled-oidc-{Guid.NewGuid():N}@integration.test";
        using var first = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });

        var provision = await CompleteExternalAsync(first, subject, email);
        Assert.Equal("/", provision.Headers.Location?.OriginalString);

        using var owner = await factory.CreateOwnerClientAsync();
        var status = await owner.GetFromJsonAsync<JsonElement>("/api/v1/identity/owner/system-status");
        Assert.True(status.GetProperty("staticOidcEnabled").GetBoolean());
        Assert.Equal("Entra", status.GetProperty("staticOidcProvider").GetString());
        Assert.NotNull(status.GetProperty("lastStaticOidcSignInAtUtc").GetString());
        Assert.DoesNotContain("identity-integration-secret", status.GetRawText(), StringComparison.Ordinal);

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var user = await db.Users.SingleAsync(item => item.Email == email);
            user.IsDisabled = true;
            await db.SaveChangesAsync();
        }

        using var second = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });
        var rejected = await CompleteExternalAsync(second, subject, email);

        Assert.Equal(HttpStatusCode.Redirect, rejected.StatusCode);
        Assert.Equal("/sign-in?error=account_locked", rejected.Headers.Location?.OriginalString);
    }

    private static async Task<HttpResponseMessage> CompleteExternalAsync(
        HttpClient client,
        string subject,
        string email)
    {
        var external = await client.GetAsync(
            $"/test/oidc-external?sub={Uri.EscapeDataString(subject)}&email={Uri.EscapeDataString(email)}");
        Assert.Equal(WorkforceOidcOptions.CompletionPath, external.Headers.Location?.OriginalString);
        return await client.GetAsync(external.Headers.Location);
    }

    private static Dictionary<string, string> ParseAuthorization(Uri location)
    {
        Assert.Equal("https", location.Scheme);
        Assert.Contains("login.microsoftonline.com", location.Host, StringComparison.Ordinal);
        var values = location.Query.TrimStart('?').Split('&', StringSplitOptions.RemoveEmptyEntries)
            .Select(item => item.Split('=', 2))
            .ToDictionary(item => Uri.UnescapeDataString(item[0]),
                item => Uri.UnescapeDataString(item.Length == 2 ? item[1] : string.Empty));
        Assert.Contains("redirect_uri", values.Keys);
        return values;
    }
}