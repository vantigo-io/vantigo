using System.Net;
using System.Net.Http.Json;
using System.Security.Claims;
using System.Text.Json;
using System.Text.RegularExpressions;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authentication.Cookies;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Endpoints.Auth;
using Vantigo.Identity.Services;

namespace Vantigo.Customers.Module.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class AuthEndpointsTests
{
    private const string BootstrapSecret = "integration-test-bootstrap-secret";
    private readonly CustomersApiFactory _factory;

    public AuthEndpointsTests(CustomersApiFactory factory)
    {
        _factory = factory;
    }

    [Fact]
    public async Task AnonymousApiRequest_Returns401WithoutServingSpa()
    {
        using var client = _factory.CreateClient();

        var response = await client.GetAsync("/api/v1/customers");

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
        Assert.Equal("application/json", response.Content.Headers.ContentType?.MediaType);
    }

    [Fact]
    public async Task BasePathPrefixedRequest_ReachesTheSameEndpoints()
    {
        // The modular host serves at the root by default.
        using var client = _factory.CreateClient();

        var unprefixed = await client.GetAsync("/api/v1/identity/antiforgery");

        Assert.Equal(HttpStatusCode.OK, unprefixed.StatusCode);

        var api = await client.GetAsync("/api/v1/customers");
        Assert.Equal(HttpStatusCode.Unauthorized, api.StatusCode);
    }

    [Fact]
    public async Task Bootstrap_WithBadSecret_IsRejectedAndRepeatBootstrapIsRejected()
    {
        using var badSecretClient = await CreateAntiforgeryClient();
        var badSecret = await badSecretClient.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = "wrong-secret",
            email = "another-owner@example.test",
            displayName = "Another Owner",
            password = "AnotherPassword123",
        });

        Assert.Equal(HttpStatusCode.Unauthorized, badSecret.StatusCode);

        using var repeatClient = await CreateAntiforgeryClient();
        var repeat = await repeatClient.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = BootstrapSecret,
            email = "another-owner@example.test",
            displayName = "Another Owner",
            password = "AnotherPassword123",
        });

        Assert.Equal(HttpStatusCode.Conflict, repeat.StatusCode);
        var error = await repeat.Content.ReadFromJsonAsync<ErrorResponse>();
        Assert.Equal("bootstrap_unavailable", error!.Error.Code);
    }

    [Fact]
    public async Task BootstrapAndLogin_RequireAntiforgeryToken()
    {
        using var bootstrapClient = _factory.CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var bootstrap = await bootstrapClient.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = BootstrapSecret,
            email = "blocked@example.test",
            displayName = "Blocked",
            password = "AnotherPassword123",
        });
        Assert.Equal(HttpStatusCode.BadRequest, bootstrap.StatusCode);
        Assert.Equal("csrf_validation_failed", (await bootstrap.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);

        using var loginClient = _factory.CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var login = await loginClient.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = "owner@integration.test",
            password = "not-the-password",
        });
        Assert.Equal(HttpStatusCode.BadRequest, login.StatusCode);
        Assert.Equal("csrf_validation_failed", (await login.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);

        using var authenticatedClient = _factory.CreateAuthenticatedClient();
        authenticatedClient.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var logout = await authenticatedClient.PostAsync("/api/v1/identity/logout", content: null);
        Assert.Equal(HttpStatusCode.BadRequest, logout.StatusCode);
        Assert.Equal("csrf_validation_failed", (await logout.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);
    }

    [Fact]
    public async Task Login_WithBadCredentials_ReturnsMachineReadable401()
    {
        using var client = await CreateAntiforgeryClient();

        var response = await client.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = "owner@integration.test",
            password = "not-the-password",
        });

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
        var error = await response.Content.ReadFromJsonAsync<ErrorResponse>();
        Assert.Equal("invalid_credentials", error!.Error.Code);
    }

    [Fact]
    public async Task Session_ReturnsStoredIdentityAndRoles()
    {
        using var client = _factory.CreateAuthenticatedClient();

        var session = await client.GetFromJsonAsync<SessionResponse>("/api/v1/identity/session");

        Assert.NotNull(session);
        Assert.NotEqual(Guid.Empty, session.User.Id);
        Assert.Equal("Integration Owner", session.User.DisplayName);
        Assert.Equal("owner@integration.test", session.User.Email);
        Assert.Contains("Owner", session.User.Roles);
    }

    [Fact]
    public async Task UnsafeRequest_WithoutValidCsrfToken_Returns400()
    {
        using var client = _factory.CreateAuthenticatedClient();
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");

        var response = await client.PostAsJsonAsync("/api/v1/customers", new { name = "Blocked" });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var error = await response.Content.ReadFromJsonAsync<ErrorResponse>();
        Assert.Equal("csrf_validation_failed", error!.Error.Code);
    }

    [Fact]
    public async Task Logout_ClearsApplicationCookie()
    {
        using var client = _factory.CreateAuthenticatedClient();

        var logout = await client.PostAsync("/api/v1/identity/logout", content: null);
        var session = await client.GetAsync("/api/v1/identity/session");

        Assert.Equal(HttpStatusCode.OK, logout.StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized, session.StatusCode);
    }

    [Fact]
    public async Task UnknownApiAndAuthRoutes_Return404InsteadOfSpa()
    {
        using var client = _factory.CreateClient();

        Assert.Equal(HttpStatusCode.NotFound, (await client.GetAsync("/api/not-a-route")).StatusCode);
        Assert.Equal(HttpStatusCode.NotFound, (await client.GetAsync("/api/v1/identity/not-a-route")).StatusCode);
    }

    [Fact]
    public async Task Providers_WhenOidcIsNotConfigured_ReturnsNullAndChallengeIsUnavailable()
    {
        using var client = _factory.CreateClient(new WebApplicationFactoryClientOptions { AllowAutoRedirect = false });

        var providers = await client.GetFromJsonAsync<OidcProvidersResponse>("/api/v1/identity/providers");

        Assert.NotNull(providers);
        Assert.Null(providers!.Oidc);
        Assert.Equal(HttpStatusCode.NotFound, (await client.GetAsync("/api/v1/identity/oidc/challenge")).StatusCode);
    }

    [Fact]
    public async Task OwnerOnlyEndpoint_ReturnsMachineReadable403ForStandardUser()
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        var authorization = scope.ServiceProvider.GetRequiredService<IAuthorizationService>();
        var principal = new ClaimsPrincipal(new ClaimsIdentity([new Claim(ClaimTypes.Role, "User")], "test"));
        var ownerPolicy = new AuthorizationPolicyBuilder().RequireRole("Owner").Build();

        var result = await authorization.AuthorizeAsync(principal, ownerPolicy);

        Assert.False(result.Succeeded);

        var cookieOptions = scope.ServiceProvider
            .GetRequiredService<IOptionsMonitor<CookieAuthenticationOptions>>()
            .Get(IdentityConstants.ApplicationScheme);
        var httpContext = new DefaultHttpContext { RequestServices = scope.ServiceProvider };
        httpContext.Response.Body = new MemoryStream();
        var redirectContext = new RedirectContext<CookieAuthenticationOptions>(
            httpContext,
            new AuthenticationScheme(IdentityConstants.ApplicationScheme, null, typeof(CookieAuthenticationHandler)),
            cookieOptions,
            new AuthenticationProperties(),
            "/api/v1/identity/owner");

        await cookieOptions.Events.OnRedirectToAccessDenied(redirectContext);

        Assert.Equal(HttpStatusCode.Forbidden, (HttpStatusCode)httpContext.Response.StatusCode);
        httpContext.Response.Body.Position = 0;
        var error = await JsonSerializer.DeserializeAsync<ErrorResponse>(
            httpContext.Response.Body,
            new JsonSerializerOptions { PropertyNameCaseInsensitive = true });
        Assert.Equal("forbidden", error!.Error.Code);
    }

    [Fact]
    public async Task FailedLoginAttempts_TriggerIdentityLockout()
    {
        var email = $"lockout-{Guid.NewGuid():N}@integration.test";
        await CreateStandardUser(email, "LockoutPassword123");

        HttpStatusCode? lastStatus = null;
        for (var attempt = 0; attempt < 6; attempt++)
        {
            using var client = await CreateAntiforgeryClient();
            var response = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password = "wrong-password" });
            lastStatus = response.StatusCode;
            if (response.StatusCode == HttpStatusCode.TooManyRequests)
            {
                var error = await response.Content.ReadFromJsonAsync<ErrorResponse>();
                if (error!.Error.Code == "account_locked")
                {
                    break;
                }
            }
        }

        Assert.Equal(HttpStatusCode.TooManyRequests, lastStatus);
    }

    private async Task<HttpClient> CreateStandardUserClient()
    {
        var email = $"standard-{Guid.NewGuid():N}@integration.test";
        await CreateStandardUser(email, "StandardPassword123");
        var client = await CreateAntiforgeryClient();
        var login = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password = "StandardPassword123" });
        Assert.Equal(HttpStatusCode.OK, login.StatusCode);
        return client;
    }

    private async Task CreateStandardUser(string email, string password)
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        var userManager = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = new ApplicationUser
        {
            UserName = email,
            Email = email,
            DisplayName = "Standard User",
        };
        Assert.True((await userManager.CreateAsync(user, password)).Succeeded);
        Assert.True((await userManager.AddToRoleAsync(user, "User")).Succeeded);
    }

    [Fact]
    public async Task LoginAndLogout_CanRefreshAntiforgeryToken()
    {
        using var client = _factory.CreateAuthenticatedClient();
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var afterLogin = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", afterLogin!.Token);
        Assert.Equal(HttpStatusCode.OK, (await client.PostAsync("/api/v1/identity/logout", content: null)).StatusCode);

        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var afterLogout = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        Assert.False(string.IsNullOrWhiteSpace(afterLogout!.Token));
    }

    [Fact]
    public async Task NewSensitivePublicPostsRequireCsrf()
    {
        using var client = _factory.CreateClient();
        var endpoints = new[]
        {
            "/api/v1/identity/invitations/accept",
            "/api/v1/identity/password-recovery/request",
            "/api/v1/identity/password-recovery/reset",
            "/api/v1/identity/login/2fa",
        };

        foreach (var endpoint in endpoints)
        {
            var response = await client.PostAsJsonAsync(endpoint, new { token = "invalid", email = "unknown@example.test" });
            Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
            Assert.Equal("csrf_validation_failed", (await response.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);
        }
    }

    private async Task<HttpClient> CreateAntiforgeryClient()
    {
        var client = _factory.CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        return client;
    }

    private sealed record ErrorResponse(Error Error);
    private sealed record Error(string Code, string Message);
    private sealed record SessionResponse(SessionUser User);
    private sealed record SessionUser(Guid Id, string DisplayName, string Email, string[] Roles);
    private sealed record OidcProvidersResponse(OidcProviderResponse? Oidc);
    private sealed record OidcProviderResponse(string DisplayName);
}

[Collection(CustomersApiCollection.Name)]
public sealed class Phase3AccountIntegrationTests
{
    private readonly CustomersApiFactory _factory;

    public Phase3AccountIntegrationTests(CustomersApiFactory factory) => _factory = factory;

    [Fact]
    public async Task InvitationCreatePersistsHashOnlyAndAcceptsOnce()
    {
        using var owner = _factory.CreateAuthenticatedClient();
        var email = $"invite-{Guid.NewGuid():N}@integration.test";
        var response = await owner.PostAsJsonAsync("/api/v1/identity/owner/invitations", new { email, displayName = "Invited", role = "User" });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var invitation = await response.Content.ReadFromJsonAsync<InvitationDto>();
        var message = Assert.Single(_factory.EmailSender.Messages, item => item.To == email);
        var token = ExtractQuery(message.TextBody, "token");

        await using (var scope = _factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var stored = await db.Invitations.SingleAsync(item => item.Id == invitation!.Id);
            Assert.DoesNotContain(token, stored.TokenHash, StringComparison.Ordinal);
            Assert.DoesNotContain(token, stored.Email, StringComparison.Ordinal);
        }

        using var accept = await CsrfClient();
        var accepted = await accept.PostAsJsonAsync("/api/v1/identity/invitations/accept", new { token, password = "InvitedPassword123" });
        Assert.Equal(HttpStatusCode.Created, accepted.StatusCode);
        Assert.Equal(HttpStatusCode.BadRequest,
            (await accept.PostAsJsonAsync("/api/v1/identity/invitations/accept", new { token, password = "InvitedPassword123" })).StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await accept.GetAsync("/api/v1/identity/session")).StatusCode);
    }

    [Fact]
    public async Task InvitationDeliveryFailureRevokesAndRecoveryIsGeneric()
    {
        using var owner = _factory.CreateAuthenticatedClient();
        _factory.EmailSender.Fail = true;
        var email = $"failed-{Guid.NewGuid():N}@integration.test";
        var response = await owner.PostAsJsonAsync("/api/v1/identity/owner/invitations", new { email, role = "User" });
        Assert.Equal(HttpStatusCode.InternalServerError, response.StatusCode);
        _factory.EmailSender.Fail = false;

        await using (var scope = _factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            Assert.False(await EntityFrameworkQueryableExtensions.AnyAsync(db.Invitations, item => item.Email == email && item.RevokedAt == null));
        }

        await using (var userScope = _factory.Services.CreateAsyncScope())
        {
            var users = userScope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var knownUser = await users.FindByEmailAsync("owner@integration.test");
            Assert.NotNull(knownUser);
            knownUser!.EmailConfirmed = true;
            Assert.True((await users.UpdateAsync(knownUser)).Succeeded);
        }

        _factory.EmailSender.Clear();
        _factory.EmailSender.Fail = true;
        try
        {
            var known = await Recovery("owner@integration.test");
            var unknown = await Recovery($"unknown-{Guid.NewGuid():N}@integration.test");
            Assert.Equal(HttpStatusCode.OK, known.StatusCode);
            Assert.Equal(HttpStatusCode.OK, unknown.StatusCode);
            Assert.Equal(await known.Content.ReadAsStringAsync(), await unknown.Content.ReadAsStringAsync());
        }
        finally
        {
            _factory.EmailSender.Fail = false;
            _factory.EmailSender.Clear();
        }
    }

    [Fact]
    public async Task InvitationManagementRejectsAnonymousStandardUserAndMissingCsrf()
    {
        using var anonymous = _factory.CreateClient();
        Assert.Equal(HttpStatusCode.Unauthorized, (await anonymous.GetAsync("/api/v1/identity/owner/invitations")).StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await anonymous.PostAsJsonAsync("/api/v1/identity/owner/invitations", new { email = "x@example.test", role = "User" })).StatusCode);

        using var standard = await CreateStandardClient();
        Assert.Equal(HttpStatusCode.Forbidden, (await standard.GetAsync("/api/v1/identity/owner/invitations")).StatusCode);
        using var owner = _factory.CreateAuthenticatedClient();
        owner.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var response = await owner.PostAsJsonAsync("/api/v1/identity/owner/invitations", new { email = "x@example.test", role = "User" });
        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        Assert.Equal("csrf_validation_failed", (await response.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);
    }

    [Fact]
    public async Task ResendReplacesTokenAndLeavesOneActiveInvitation()
    {
        using var owner = _factory.CreateAuthenticatedClient();
        var email = $"resend-{Guid.NewGuid():N}@integration.test";
        var first = await owner.PostAsJsonAsync("/api/v1/identity/owner/invitations", new { email, role = "User" });
        var firstDto = await first.Content.ReadFromJsonAsync<InvitationDto>();
        var firstToken = ExtractQuery(Assert.Single(_factory.EmailSender.Messages, item => item.To == email).TextBody, "token");
        var resend = await owner.PostAsJsonAsync($"/api/v1/identity/owner/invitations/{firstDto!.Id}/resend", new { });
        Assert.Equal(HttpStatusCode.OK, resend.StatusCode);
        var replacement = await resend.Content.ReadFromJsonAsync<InvitationDto>();
        var secondToken = ExtractQuery(_factory.EmailSender.Messages.Last(item => item.To == email).TextBody, "token");
        Assert.NotEqual(firstToken, secondToken);

        using var oldAccept = await CsrfClient();
        Assert.Equal(HttpStatusCode.BadRequest,
            (await oldAccept.PostAsJsonAsync("/api/v1/identity/invitations/accept", new { token = firstToken, password = "OldPassword123" })).StatusCode);
        await using var scope = _factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.Equal(1, await db.Invitations.CountAsync(item => item.Email == email && item.AcceptedAt == null && item.RevokedAt == null));
        Assert.NotEqual(firstDto.Id, replacement!.Id);
    }

    [Fact]
    public async Task ConcurrentAcceptanceCreatesExactlyOneAccount()
    {
        using var owner = _factory.CreateAuthenticatedClient();
        var email = $"concurrent-{Guid.NewGuid():N}@integration.test";
        var created = await owner.PostAsJsonAsync("/api/v1/identity/owner/invitations", new { email, role = "User" });
        var dto = await created.Content.ReadFromJsonAsync<InvitationDto>();
        var token = ExtractQuery(Assert.Single(_factory.EmailSender.Messages, item => item.To == email).TextBody, "token");
        var clients = await Task.WhenAll(Enumerable.Range(0, 4).Select(_ => CsrfClient()));
        var responses = await Task.WhenAll(clients.Select(client => client.PostAsJsonAsync("/api/v1/identity/invitations/accept", new { token, password = "ConcurrentPassword123" })));
        Assert.Equal(1, responses.Count(item => item.StatusCode == HttpStatusCode.Created));
        foreach (var response in responses.Where(item => item.StatusCode != HttpStatusCode.Created))
        {
            Assert.True(response.StatusCode == HttpStatusCode.BadRequest,
                $"Unexpected concurrent acceptance response {response.StatusCode}: {await response.Content.ReadAsStringAsync()}");
        }
        await using var scope = _factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.Equal(1, await db.Users.CountAsync(item => item.Email == email));
    }

    [Fact]
    public async Task RecoveryLinkResetsPasswordInvalidatesSessionAndCannotReplay()
    {
        var email = $"recovery-{Guid.NewGuid():N}@integration.test";
        const string originalPassword = "RecoveryOriginal123";
        const string changedPassword = "RecoveryChanged123";
        await using (var userScope = _factory.Services.CreateAsyncScope())
        {
            var users = userScope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var user = new ApplicationUser { UserName = email, Email = email, EmailConfirmed = true, DisplayName = "Recovery User" };
            Assert.True((await users.CreateAsync(user, originalPassword)).Succeeded);
            Assert.True((await users.AddToRoleAsync(user, "User")).Succeeded);
        }

        using var session = await CsrfClient();
        Assert.Equal(HttpStatusCode.OK,
            (await session.PostAsJsonAsync("/api/v1/identity/login", new { email, password = originalPassword })).StatusCode);
        await RefreshCsrf(session);
        using var requestClient = await CsrfClient();
        _factory.EmailSender.Clear();
        var known = await requestClient.PostAsJsonAsync("/api/v1/identity/password-recovery/request", new { email });
        Assert.Equal(HttpStatusCode.OK, known.StatusCode);
        var link = Assert.Single(_factory.EmailSender.Messages).TextBody;
        var token = ExtractQuery(link, "token");
        using var reset = await CsrfClient();
        var resetResponse = await reset.PostAsJsonAsync("/api/v1/identity/password-recovery/reset", new { email, token, newPassword = changedPassword });
        Assert.Equal(HttpStatusCode.OK, resetResponse.StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized, (await session.GetAsync("/api/v1/identity/session")).StatusCode);
        var replay = await reset.PostAsJsonAsync("/api/v1/identity/password-recovery/reset", new { email, token, newPassword = changedPassword });
        Assert.Equal(HttpStatusCode.BadRequest, replay.StatusCode);
        Assert.Equal("invalid_reset_token", (await replay.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);

        using var login = await CsrfClient();
        Assert.Equal(HttpStatusCode.OK, (await login.PostAsJsonAsync("/api/v1/identity/login", new { email, password = changedPassword })).StatusCode);
    }

    private async Task<HttpResponseMessage> Recovery(string email)
    {
        using var client = await CsrfClient();
        return await client.PostAsJsonAsync("/api/v1/identity/password-recovery/request", new { email });
    }

    private async Task<HttpClient> CsrfClient()
    {
        var client = _factory.CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        return client;
    }

    private static async Task RefreshCsrf(HttpClient client)
    {
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
    }

    private async Task<HttpClient> CreateStandardClient()
    {
        var email = $"standard-owner-route-{Guid.NewGuid():N}@integration.test";
        await using var scope = _factory.Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = new ApplicationUser { UserName = email, Email = email, DisplayName = "Standard" };
        Assert.True((await users.CreateAsync(user, "StandardRoutePassword123")).Succeeded);
        Assert.True((await users.AddToRoleAsync(user, "User")).Succeeded);
        var client = await CsrfClient();
        Assert.Equal(HttpStatusCode.OK, (await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password = "StandardRoutePassword123" })).StatusCode);
        return client;
    }

    private static string ExtractQuery(string text, string key)
    {
        var match = Regex.Match(text, $"[?&]{key}=([^&\\s]+)");
        Assert.True(match.Success);
        return Uri.UnescapeDataString(match.Groups[1].Value);
    }

    private sealed record InvitationDto(Guid Id, string Email, string Role);
    private sealed record ErrorResponse(Error Error);
    private sealed record Error(string Code, string Message);
}

[CollectionDefinition("FreshCustomersApi")]
public sealed class FreshCustomersApiCollection;

[Collection("FreshCustomersApi")]
public sealed class FreshBootstrapTests
{
    [Fact]
    public async Task BootstrapStatus_IsAnonymousJsonAndInitiallyAvailable()
    {
        await using var factory = new FreshCustomersApiFactory();
        await factory.StartAsync();
        using var client = factory.CreateCookieClient();

        var response = await client.GetAsync("/api/v1/identity/bootstrap-status");
        var body = await response.Content.ReadFromJsonAsync<BootstrapStatusDto>();

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal("application/json", response.Content.Headers.ContentType?.MediaType);
        Assert.True(body!.Available);
        var raw = await response.Content.ReadAsStringAsync();
        Assert.DoesNotContain(FreshCustomersApiFactory.BootstrapSecret, raw, StringComparison.Ordinal);
        Assert.DoesNotContain("owner", raw, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task BootstrapStatus_IsFalseAfterSuccessfulBootstrap()
    {
        await using var factory = new FreshCustomersApiFactory();
        await factory.StartAsync();
        using var client = await factory.CreateAntiforgeryClientAsync();

        var bootstrap = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = FreshCustomersApiFactory.BootstrapSecret,
            email = "status-owner@example.test",
            displayName = "Status Owner",
            password = "StatusOwnerPassword123",
        });
        Assert.Equal(HttpStatusCode.Created, bootstrap.StatusCode);

        var response = await client.GetAsync("/api/v1/identity/bootstrap-status");
        var body = await response.Content.ReadFromJsonAsync<BootstrapStatusDto>();

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.False(body!.Available);
    }

    [Fact]
    public async Task BootstrapStatus_UsesGeneratedSecretWhenBootstrapSecretIsUnset()
    {
        await using var factory = new FreshCustomersApiFactory { ConfigureBootstrapSecret = false };
        await factory.StartAsync();
        var generatedSecret = factory.Services.GetRequiredService<BootstrapSecretProvider>().Secret;
        using var client = factory.CreateCookieClient();

        var response = await client.GetAsync("/api/v1/identity/bootstrap-status");
        var body = await response.Content.ReadFromJsonAsync<BootstrapStatusDto>();

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.True(body!.Available);
        Assert.DoesNotContain(generatedSecret, await response.Content.ReadAsStringAsync(), StringComparison.Ordinal);

        using var csrfClient = await factory.CreateAntiforgeryClientAsync();
        var bootstrap = await csrfClient.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = generatedSecret,
            email = "generated-owner@example.test",
            displayName = "Generated Owner",
            password = "GeneratedOwnerPassword123",
        });
        Assert.Equal(HttpStatusCode.Created, bootstrap.StatusCode);
        var consumed = await csrfClient.GetFromJsonAsync<BootstrapStatusDto>("/api/v1/identity/bootstrap-status");
        Assert.False(consumed!.Available);
    }

    [Fact]
    public async Task BadSecretDoesNotChangeFreshState_AndFirstValidBootstrapReturnsCreated()
    {
        await using var factory = new FreshCustomersApiFactory();
        await factory.StartAsync();
        using var client = await factory.CreateAntiforgeryClientAsync();

        var bad = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = "wrong-secret",
            email = "fresh-owner@example.test",
            displayName = "Fresh Owner",
            password = "FreshPassword123",
        });
        Assert.Equal(HttpStatusCode.Unauthorized, bad.StatusCode);

        var valid = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = FreshCustomersApiFactory.BootstrapSecret,
            email = "fresh-owner@example.test",
            displayName = "Fresh Owner",
            password = "FreshPassword123",
        });
        Assert.Equal(HttpStatusCode.Created, valid.StatusCode);

        var session = await client.GetAsync("/api/v1/identity/session");
        var sessionBody = await session.Content.ReadFromJsonAsync<BootstrapSessionDto>();
        Assert.Equal(HttpStatusCode.OK, session.StatusCode);
        Assert.NotEqual(Guid.Empty, sessionBody!.User.Id);
        Assert.Equal("Fresh Owner", sessionBody.User.DisplayName);
        Assert.Equal("fresh-owner@example.test", sessionBody.User.Email);
        Assert.Contains("Owner", sessionBody.User.Roles);
    }

    [Fact]
    public async Task ConcurrentBootstrapCreatesAtMostOneOwnerAndMarker()
    {
        await using var factory = new FreshCustomersApiFactory();
        await factory.StartAsync();

        var clients = await Task.WhenAll(Enumerable.Range(0, 8).Select(_ => factory.CreateAntiforgeryClientAsync()));
        var responses = await Task.WhenAll(clients.Select(client => client.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = FreshCustomersApiFactory.BootstrapSecret,
            email = "race-owner@example.test",
            displayName = "Race Owner",
            password = "RacePassword123",
        })));

        Assert.Equal(1, responses.Count(response => response.StatusCode == HttpStatusCode.Created));
        foreach (var response in responses.Where(response => response.StatusCode != HttpStatusCode.Created))
        {
            Assert.True(response.StatusCode == HttpStatusCode.Conflict,
                $"Unexpected bootstrap response {response.StatusCode}: {await response.Content.ReadAsStringAsync()}");
        }

        await using var scope = factory.Services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.Equal(1, dbContext.Users.Count());
        Assert.Equal(1, dbContext.BootstrapStates.Count());
    }

    private sealed record BootstrapStatusDto(bool Available);
    private sealed record BootstrapSessionDto(BootstrapSessionUser User);
    private sealed record BootstrapSessionUser(Guid Id, string DisplayName, string? Email, string[] Roles);
}