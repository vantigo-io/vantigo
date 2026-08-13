using System.IdentityModel.Tokens.Jwt;
using System.Net;
using System.Net.Http.Headers;
using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.WebUtilities;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.IdentityModel.Tokens;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityOidcApiCollection.Name)]
public sealed class DynamicFederationRuntimeSecurityTests(OidcIdentityApiFactory factory)
{
    private const string Issuer = "https://issuer.runtime.test";
    private const string ClientId = "runtime-client";
    private const string SecretReference = "VANTIGO_SSO_INTEGRATION_CLIENT_SECRET";
    private static readonly RsaSecurityKey SigningKey = new(RSA.Create(2048)) { KeyId = "runtime-key" };

    [Fact]
    public async Task RealChallengeAndCallback_JitPersistsCorrelationAuditAndNoAuthorization()
    {
        var connection = await CreateConnectionAsync(FederationProviderKind.Generic);
        var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });

        var challenge = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        Assert.Equal(HttpStatusCode.Redirect, challenge.StatusCode);
        var authorization = challenge.Headers.Location!;
        var query = QueryHelpers.ParseQuery(authorization.Query);
        var state = query["state"].ToString();
        var nonce = query["nonce"].ToString();
        Assert.NotNull(state);
        Assert.Contains("code_challenge=", authorization.Query, StringComparison.Ordinal);
        Assert.Contains("nonce=", authorization.Query, StringComparison.Ordinal);
        Assert.DoesNotContain("integration-federation-secret", authorization.ToString(), StringComparison.Ordinal);
        Assert.Contains(challenge.Headers.GetValues("Set-Cookie"), value => value.Contains("vantigo.identity.federation.correlation.", StringComparison.Ordinal));

        DynamicFederationTestTransport.Responder = request => request.RequestUri!.AbsolutePath.EndsWith("/token", StringComparison.Ordinal)
            ? JsonResponse(JsonSerializer.Serialize(new { id_token = CreateToken(nonce, "runtime-subject", "runtime.user@example.com") }))
            : JsonResponse(JsonWebKeySetJson(), "application/json");
        var parsedKeys = new JsonWebKeySet(JsonWebKeySetJson()).GetSigningKeys().ToArray();
        Assert.NotEmpty(parsedKeys);
        Assert.True(DynamicFederationOidcService.TryValidateIdToken(CreateToken(nonce, "runtime-subject", "runtime.user@example.com"), parsedKeys, Connection(FederationProviderKind.Generic), Issuer, nonce, out _));
        var localToken = CreateToken(nonce, "runtime-subject", "runtime.user@example.com");
        Assert.True(DynamicFederationOidcService.TryValidateIdToken(localToken, [SigningKey], Connection(FederationProviderKind.Generic), Issuer, nonce, out _));

        try
        {
            var callback = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(state)}&code=opaque-provider-code");
            Assert.Equal(HttpStatusCode.Redirect, callback.StatusCode);
            if (callback.Headers.Location?.OriginalString != "/")
            {
                await using var debugScope = factory.Services.CreateAsyncScope();
                var debugDb = debugScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
                var debugRows = await debugDb.FederatedIdentities.Select(item => item.Subject).ToArrayAsync();
                Assert.Fail($"Callback failed; outbound federation requests: {string.Join(",", DynamicFederationTestTransport.Requests)}; identities: {string.Join(",", debugRows)}");
            }
            Assert.Equal("/", callback.Headers.Location?.OriginalString);
            Assert.DoesNotContain("integration-federation-secret", string.Join('|', callback.Headers.SelectMany(pair => pair.Value)), StringComparison.Ordinal);

            await using var scope = factory.Services.CreateAsyncScope();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var identity = await db.FederatedIdentities.SingleAsync(item => item.ConnectionId == connection.Id && item.Subject == "runtime-subject");
            var user = await db.Users.SingleAsync(item => item.Id == identity.UserId);
            Assert.Equal([], await db.UserRoles.Where(item => item.UserId == user.Id).ToArrayAsync());
            Assert.Equal([], await db.AccessGroupMemberships.Where(item => item.UserId == user.Id).ToArrayAsync());
            Assert.Equal([], await db.AccessGroupRoleMappings.Where(item => false).ToArrayAsync());
            var audit = await db.AuthorizationAuditEvents.Where(item => item.Action == "federation.identity-jit-created").SingleAsync();
            Assert.DoesNotContain("integration-federation-secret", audit.BeforeJson + audit.AfterJson + audit.Details, StringComparison.Ordinal);

            var replay = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(state)}&code=opaque-provider-code");
            Assert.Equal("/sign-in?error=federation_sign_in_failed", replay.Headers.Location?.OriginalString);
        }
        finally
        {
            DynamicFederationTestTransport.Responder = null;
        }
    }

    [Fact]
    public async Task ParallelChallenges_UseIndependentCookieNamesAndBothStatesPersist()
    {
        var connection = await CreateConnectionAsync(FederationProviderKind.Generic);
        var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });
        var first = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        var second = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        var firstCookie = first.Headers.GetValues("Set-Cookie").Single(value => value.Contains("correlation.", StringComparison.Ordinal)).Split('=', 2)[0];
        var secondCookie = second.Headers.GetValues("Set-Cookie").Single(value => value.Contains("correlation.", StringComparison.Ordinal)).Split('=', 2)[0];
        Assert.NotEqual(firstCookie, secondCookie);

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.True(await db.FederationOidcStates.CountAsync() >= 2);
    }

    [Fact]
    public async Task CompletingFlowADeletesOnlyCookieAAndFlowBCanStillCallback()
    {
        var connection = await CreateConnectionAsync(FederationProviderKind.Generic);
        var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions { AllowAutoRedirect = false, HandleCookies = true });
        var flowA = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        var flowB = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        var queryA = QueryHelpers.ParseQuery(flowA.Headers.Location!.Query);
        var queryB = QueryHelpers.ParseQuery(flowB.Headers.Location!.Query);
        var cookieA = flowA.Headers.GetValues("Set-Cookie").Single(value => value.Contains("correlation.", StringComparison.Ordinal)).Split('=', 2)[0];
        var cookieB = flowB.Headers.GetValues("Set-Cookie").Single(value => value.Contains("correlation.", StringComparison.Ordinal)).Split('=', 2)[0];
        var cookieAHeader = flowA.Headers.GetValues("Set-Cookie").Single(value => value.StartsWith(cookieA + "=", StringComparison.Ordinal)).Split(';', 2)[0];
        var cookieBHeader = flowB.Headers.GetValues("Set-Cookie").Single(value => value.StartsWith(cookieB + "=", StringComparison.Ordinal)).Split(';', 2)[0];
        Assert.NotEqual(cookieA, cookieB);
        DynamicFederationTestTransport.Responder = request =>
        {
            var flowB = request.Content?.ReadAsStringAsync().GetAwaiter().GetResult().Contains("flow-b", StringComparison.Ordinal) == true;
            return request.RequestUri!.AbsolutePath.EndsWith("/token", StringComparison.Ordinal)
                ? JsonResponse(JsonSerializer.Serialize(new { id_token = CreateToken(flowB ? queryB["nonce"].ToString() : queryA["nonce"].ToString(), flowB ? "flow-subject-b" : "flow-subject-a", flowB ? "flow-b@example.com" : "flow-a@example.com") }))
                : JsonResponse(JsonWebKeySetJson());
        };
        try
        {
            client.DefaultRequestHeaders.Remove("Cookie");
            client.DefaultRequestHeaders.Add("Cookie", $"{cookieAHeader}; {cookieBHeader}");
            var callbackA = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(queryA["state"])}&code=flow-a");
            Assert.Equal("/", callbackA.Headers.Location?.OriginalString);
            Assert.Contains(callbackA.Headers.GetValues("Set-Cookie"), value => value.StartsWith($"{cookieA}=", StringComparison.Ordinal) && value.Contains("expires=Thu, 01 Jan 1970", StringComparison.OrdinalIgnoreCase));
            Assert.DoesNotContain(callbackA.Headers.GetValues("Set-Cookie"), value => value.StartsWith($"{cookieB}=", StringComparison.Ordinal));
            var callbackB = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(queryB["state"])}&code=flow-b");
            Assert.True(callbackB.Headers.Location?.OriginalString == "/", $"Flow B failed: {callbackB.Headers.Location}; requests={string.Join('|', DynamicFederationTestTransport.RequestBodies)}");
        }
        finally { DynamicFederationTestTransport.Responder = null; client.DefaultRequestHeaders.Remove("Cookie"); }
    }

    [Fact]
    public void JwtValidation_RequiresRawClaimsIssuerAudienceAzpAndNonce()
    {
        var connection = Connection(FederationProviderKind.Generic);
        var valid = CreateToken("nonce", "subject", "user@example.com", [ClientId, "other"], ClientId);
        Assert.True(DynamicFederationOidcService.TryValidateIdToken(valid, [SigningKey], connection, Issuer, "nonce", out var principal));
        Assert.Equal("subject", principal!.FindFirst("sub")?.Value);
        Assert.False(DynamicFederationOidcService.TryValidateIdToken(
            CreateToken("nonce", "subject", "user@example.com", [ClientId, "other"], null), [SigningKey], connection, Issuer, "nonce", out _));
        Assert.False(DynamicFederationOidcService.TryValidateIdToken(
            CreateToken("wrong", "subject", "user@example.com"), [SigningKey], connection, Issuer, "nonce", out _));
        Assert.False(DynamicFederationOidcService.TryValidateIdToken(
            CreateToken("nonce", "subject", "user@example.com", ["other"], null), [SigningKey], connection, Issuer, "nonce", out _));
    }

    [Fact]
    public void ProviderPolicies_RequireEntraCorrelationAndGoogleHd()
    {
        Assert.True(DynamicFederationOidcService.ValidateEntraClaims(Principal(("tid", Tenant), ("oid", ObjectId)), EntraIssuer));
        Assert.False(DynamicFederationOidcService.ValidateEntraClaims(Principal(("tid", "33333333-3333-3333-3333-333333333333"), ("oid", ObjectId)), EntraIssuer));
        var google = Connection(FederationProviderKind.Google, ["example.com"]);
        Assert.False(DynamicFederationOidcService.AllowedDomain(new DynamicFederationOidcService.ExternalIdentity(Issuer, "s", "u@example.com", true, "u", null, null, null), google));
        Assert.False(DynamicFederationOidcService.AllowedDomain(new DynamicFederationOidcService.ExternalIdentity(Issuer, "s", "u@example.com", true, "u", "other.com", null, null), google));
        Assert.True(DynamicFederationOidcService.AllowedDomain(new DynamicFederationOidcService.ExternalIdentity(Issuer, "s", "u@example.com", true, "u", "example.com", null, null), google));
        Assert.False(DynamicFederationOidcService.AllowedDomain(new DynamicFederationOidcService.ExternalIdentity(Issuer, "s", "u@example.com", true, "u", "example.com", null, null), Connection(FederationProviderKind.Google)));
    }

    [Fact]
    public async Task GoogleCallbackWithEmptyAllowedDomainsFailsClosedWithoutProvisioning()
    {
        var connection = await CreateConnectionAsync(FederationProviderKind.Google);
        var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions { AllowAutoRedirect = false, HandleCookies = true });
        var challenge = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        var query = QueryHelpers.ParseQuery(challenge.Headers.Location!.Query);
        var state = query["state"].ToString();
        var nonce = query["nonce"].ToString();
        DynamicFederationTestTransport.Responder = request => request.RequestUri!.AbsolutePath.EndsWith("/token", StringComparison.Ordinal)
            ? JsonResponse(JsonSerializer.Serialize(new { id_token = CreateToken(nonce, "google-empty-domain", "u@example.com") }))
            : JsonResponse(JsonWebKeySetJson());
        try
        {
            var callback = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(state)}&code=google-code");
            Assert.Equal("/sign-in?error=federation_sign_in_failed", callback.Headers.Location?.OriginalString);
            await using var scope = factory.Services.CreateAsyncScope();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            Assert.DoesNotContain(await db.FederatedIdentities.Select(item => item.Subject).ToArrayAsync(), item => item == "google-empty-domain");
        }
        finally { DynamicFederationTestTransport.Responder = null; }
    }

    [Fact]
    public async Task LinkedMfaAccountReturnsExistingSignInContinuationContract()
    {
        var connection = await CreateConnectionAsync(FederationProviderKind.Generic);
        const string subject = "linked-mfa-subject";
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var user = new ApplicationUser { UserName = $"linked-{Guid.NewGuid():N}", Email = $"linked-{Guid.NewGuid():N}@example.com", DisplayName = "Linked MFA" };
            Assert.True((await users.CreateAsync(user)).Succeeded);
            user.TwoFactorEnabled = true;
            await users.UpdateAsync(user);
            db.FederatedIdentities.Add(new FederatedIdentity { ConnectionId = connection.Id, Issuer = Issuer, Subject = subject, UserId = user.Id, CreatedAt = DateTimeOffset.UtcNow, UpdatedAt = DateTimeOffset.UtcNow });
            await users.AddLoginAsync(user, new UserLoginInfo($"{DynamicFederationAuthentication.Scheme}:{connection.Id:N}:{Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(Issuer))).ToLowerInvariant()}", subject, connection.DisplayName));
            await db.SaveChangesAsync();
            var persisted = await db.Users.SingleAsync(item => item.Id == user.Id);
            Assert.True(persisted.TwoFactorEnabled);
        }
        var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions { AllowAutoRedirect = false, HandleCookies = true });
        var challenge = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        var query = QueryHelpers.ParseQuery(challenge.Headers.Location!.Query);
        DynamicFederationTestTransport.Responder = request => request.RequestUri!.AbsolutePath.EndsWith("/token", StringComparison.Ordinal)
            ? JsonResponse(JsonSerializer.Serialize(new { id_token = CreateToken(query["nonce"].ToString(), subject, "linked@example.com") }))
            : JsonResponse(JsonWebKeySetJson());
        try
        {
            var callback = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(query["state"])}&code=mfa-code");
            if (callback.Headers.Location?.OriginalString != "/sign-in?mfa=federation")
            {
                await using var debugScope = factory.Services.CreateAsyncScope();
                var debugDb = debugScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
                var debugIdentities = await debugDb.FederatedIdentities.Where(item => item.ConnectionId == connection.Id).Select(item => new { item.Subject, item.UserId }).ToArrayAsync();
                Assert.Fail($"Expected MFA continuation, got {callback.Headers.Location}; identities={string.Join(',', debugIdentities.Select(item => item.Subject))}");
            }
        }
        finally { DynamicFederationTestTransport.Responder = null; }
    }

    [Fact]
    public async Task ChallengeVersionChangedBeforeCallbackFailsWithoutProviderNetworkCall()
    {
        var connection = await CreateConnectionAsync(FederationProviderKind.Generic);
        var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions { AllowAutoRedirect = false, HandleCookies = true });
        var challenge = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        var state = QueryHelpers.ParseQuery(challenge.Headers.Location!.Query)["state"].ToString();
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var current = await db.FederationConnections.SingleAsync(item => item.Id == connection.Id);
            current.ConfigurationVersion++;
            await db.SaveChangesAsync();
        }
        DynamicFederationTestTransport.Requests.Clear();
        var callback = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(state)}&code=stale-code");
        Assert.Equal("/sign-in?error=federation_sign_in_failed", callback.Headers.Location?.OriginalString);
        Assert.Empty(DynamicFederationTestTransport.Requests);
    }

    [Fact]
    public async Task CallbackEmailCollisionFailsWithoutLinkOrSecondUser()
    {
        var connection = await CreateConnectionAsync(FederationProviderKind.Generic);
        var email = $"collision-{Guid.NewGuid():N}@example.com";
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            Assert.True((await users.CreateAsync(new ApplicationUser { UserName = email, Email = email, DisplayName = "Existing" })).Succeeded);
        }
        var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions { AllowAutoRedirect = false, HandleCookies = true });
        var challenge = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        var query = QueryHelpers.ParseQuery(challenge.Headers.Location!.Query);
        DynamicFederationTestTransport.Responder = request => request.RequestUri!.AbsolutePath.EndsWith("/token", StringComparison.Ordinal)
            ? JsonResponse(JsonSerializer.Serialize(new { id_token = CreateToken(query["nonce"].ToString(), "collision-subject", email) }))
            : JsonResponse(JsonWebKeySetJson());
        try
        {
            var callback = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(query["state"])}&code=collision-code");
            Assert.Equal("/sign-in?error=federation_sign_in_failed", callback.Headers.Location?.OriginalString);
            await using var scope = factory.Services.CreateAsyncScope();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            Assert.Equal(1, await db.Users.CountAsync(item => item.Email == email));
            Assert.DoesNotContain(await db.FederatedIdentities.Select(item => item.Subject).ToArrayAsync(), subject => subject == "collision-subject");
        }
        finally { DynamicFederationTestTransport.Responder = null; }
    }

    [Fact]
    public async Task CallbackJitConflictReReadsWinnerAfterTrackingReset()
    {
        var connection = await CreateConnectionAsync(FederationProviderKind.Generic);
        const string subject = "conflict-winner-subject";
        var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions { AllowAutoRedirect = false, HandleCookies = true });
        var challenge = await client.GetAsync($"/api/v1/identity/federation/{connection.Id}/challenge");
        var query = QueryHelpers.ParseQuery(challenge.Headers.Location!.Query);
        DynamicFederationTestTransport.Responder = request => request.RequestUri!.AbsolutePath.EndsWith("/token", StringComparison.Ordinal)
            ? JsonResponse(JsonSerializer.Serialize(new { id_token = CreateToken(query["nonce"].ToString(), subject, "winner@example.com") }))
            : JsonResponse(JsonWebKeySetJson());
        var winnerId = Guid.Empty;
        DynamicFederationOidcService.JitConflictInjectorAsync = async _ =>
        {
            await using var scope = factory.Services.CreateAsyncScope();
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var winner = new ApplicationUser { UserName = $"winner-{Guid.NewGuid():N}", Email = $"winner-{Guid.NewGuid():N}@example.com", EmailConfirmed = true, DisplayName = "Winner" };
            Assert.True((await users.CreateAsync(winner)).Succeeded);
            winnerId = winner.Id;
            var provider = $"{DynamicFederationAuthentication.Scheme}:{connection.Id:N}:{Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(Issuer))).ToLowerInvariant()}";
            Assert.True((await users.AddLoginAsync(winner, new UserLoginInfo(provider, subject, connection.DisplayName))).Succeeded);
            db.FederatedIdentities.Add(new FederatedIdentity { ConnectionId = connection.Id, Issuer = Issuer, Subject = subject, UserId = winner.Id, CreatedAt = DateTimeOffset.UtcNow, UpdatedAt = DateTimeOffset.UtcNow });
            await db.SaveChangesAsync();
            // Constructor order is message, severity, invariant detail, SQLSTATE.
            return new DbUpdateException("race", new Npgsql.PostgresException("race", "ERROR", "deadlock", "40P01"));
        };
        var recoveryStages = new List<string>();
        DynamicFederationOidcService.JitRecoveryObserver = recoveryStages.Add;
        try
        {
            var callback = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(query["state"])}&code=conflict-code");
            if (callback.Headers.Location?.OriginalString != "/")
            {
                await using var debugScope = factory.Services.CreateAsyncScope();
                var debugDb = debugScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
                var debugUser = await debugDb.Users.SingleOrDefaultAsync(item => item.Id == winnerId);
                var debugLogin = await debugDb.UserLogins.Where(item => item.UserId == winnerId).Select(item => new { item.LoginProvider, item.ProviderKey }).ToArrayAsync();
                Assert.Fail($"Conflict recovery failed: {callback.Headers.Location}; stages={string.Join(',', recoveryStages)}; winner={winnerId}; user={debugUser?.UserName}; disabled={debugUser?.IsDisabled}; logins={string.Join('|', debugLogin.Select(item => item.LoginProvider + ':' + item.ProviderKey))}");
            }
            await using var scope = factory.Services.CreateAsyncScope();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            Assert.Equal(winnerId, (await db.FederatedIdentities.SingleAsync(item => item.Subject == subject)).UserId);
        }
        finally
        {
            DynamicFederationOidcService.JitConflictInjector = null;
            DynamicFederationOidcService.JitConflictInjectorAsync = null;
            DynamicFederationOidcService.JitRecoveryObserver = null;
            DynamicFederationTestTransport.Responder = null;
        }
    }

    [Fact]
    public async Task OidcFirstExistingFederatedIdentityCreatesOneScimMappingWithoutEmailCorrelation()
    {
        var federation = await CreateConnectionAsync(FederationProviderKind.Entra);
        var scim = await CreateScimBindingAsync(federation.Id);
        var objectId = Guid.NewGuid();
        Guid userId;
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var user = new ApplicationUser
            {
                UserName = $"oidc-first-{Guid.NewGuid():N}",
                Email = $"oidc-first-{Guid.NewGuid():N}@example.com",
                EmailConfirmed = true,
                DisplayName = "OIDC first",
            };
            Assert.True((await users.CreateAsync(user)).Succeeded);
            userId = user.Id;
            db.FederatedIdentities.Add(new FederatedIdentity
            {
                ConnectionId = federation.Id,
                Issuer = EntraIssuer,
                Subject = $"oidc-first-{objectId:N}",
                DirectoryTenantId = Tenant,
                DirectoryObjectId = objectId,
                UserId = user.Id,
                CreatedAt = DateTimeOffset.UtcNow,
                UpdatedAt = DateTimeOffset.UtcNow,
            });
            await db.SaveChangesAsync();
        }

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var service = scope.ServiceProvider.GetRequiredService<ScimLifecycleService>();
            var mapping = await service.EnsureMappingForOidcFederatedIdentityAsync(
                scim.Id, objectId.ToString("D"), CancellationToken.None);
            Assert.NotNull(mapping);
            Assert.Equal(userId, mapping!.UserId);
            Assert.Equal(objectId.ToString("D"), mapping.ExternalId);
        }

        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.Equal(1, await verifyDb.ScimUserMappings.CountAsync(item => item.ScimConnectionId == scim.Id));
        Assert.Equal(userId, (await verifyDb.ScimUserMappings.SingleAsync(item => item.ScimConnectionId == scim.Id)).UserId);
        var audit = (await verifyDb.AuthorizationAuditEvents
            .Where(item => item.Action == "scim.mapping.oidc-created")
            .ToArrayAsync()).Single(item => item.AfterJson.Contains(scim.Id.ToString(), StringComparison.Ordinal));
        Assert.Contains(userId.ToString(), audit.AfterJson, StringComparison.Ordinal);
        Assert.Contains(objectId.ToString(), audit.AfterJson, StringComparison.Ordinal);
        Assert.Contains(Tenant, audit.AfterJson, StringComparison.Ordinal);
        Assert.Contains("oidc-first", audit.AfterJson, StringComparison.Ordinal);
        Assert.DoesNotContain("@", audit.BeforeJson + audit.AfterJson + audit.Details, StringComparison.Ordinal);
    }

    [Fact]
    public async Task OidcFirstMappingFailureRollsBackMappingAndAudit()
    {
        var federation = await CreateConnectionAsync(FederationProviderKind.Entra);
        var scim = await CreateScimBindingAsync(federation.Id);
        var objectId = Guid.NewGuid();
        Guid userId;
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var user = new ApplicationUser
            {
                UserName = $"oidc-failure-{Guid.NewGuid():N}",
                Email = $"oidc-failure-{Guid.NewGuid():N}@example.com",
                EmailConfirmed = true,
                DisplayName = "OIDC failure",
            };
            Assert.True((await users.CreateAsync(user)).Succeeded);
            userId = user.Id;
            db.FederatedIdentities.Add(new FederatedIdentity
            {
                ConnectionId = federation.Id,
                Issuer = EntraIssuer,
                Subject = $"oidc-failure-{objectId:N}",
                DirectoryTenantId = Tenant,
                DirectoryObjectId = objectId,
                UserId = user.Id,
                CreatedAt = DateTimeOffset.UtcNow,
                UpdatedAt = DateTimeOffset.UtcNow,
            });
            await db.SaveChangesAsync();
        }

        ScimLifecycleService.MutationFailureInjector = () => new DbUpdateException("injected mapping transaction failure");
        try
        {
            await using var scope = factory.Services.CreateAsyncScope();
            var service = scope.ServiceProvider.GetRequiredService<ScimLifecycleService>();
            Assert.Null(await service.EnsureMappingForOidcFederatedIdentityAsync(scim.Id, objectId.ToString("D"), CancellationToken.None));
        }
        finally
        {
            ScimLifecycleService.MutationFailureInjector = null;
        }

        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.False(await verifyDb.ScimUserMappings.AnyAsync(item => item.ScimConnectionId == scim.Id && item.UserId == userId));
        Assert.DoesNotContain((await verifyDb.AuthorizationAuditEvents.Where(item => item.Action == "scim.mapping.oidc-created").ToArrayAsync()),
            item => item.AfterJson.Contains(objectId.ToString(), StringComparison.Ordinal));
    }

    [Fact]
    public async Task ScimFirstEntraCallbackAttachesIdentityAndUsesMfaInsteadOfEmailCollision()
    {
        var federation = await CreateConnectionAsync(FederationProviderKind.Entra);
        var scim = await CreateScimBindingAsync(federation.Id);
        var objectId = Guid.NewGuid();
        var collisionEmail = $"entra-collision-{Guid.NewGuid():N}@example.com";
        Guid mappedUserId;
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var mapped = new ApplicationUser
            {
                UserName = $"scim-mapped-{Guid.NewGuid():N}",
                Email = $"scim-mapped-{Guid.NewGuid():N}@example.com",
                EmailConfirmed = true,
                DisplayName = "SCIM mapped",
            };
            Assert.True((await users.CreateAsync(mapped)).Succeeded);
            mapped.TwoFactorEnabled = true;
            Assert.True((await users.UpdateAsync(mapped)).Succeeded);
            mappedUserId = mapped.Id;
            Assert.True((await users.CreateAsync(new ApplicationUser
            {
                UserName = collisionEmail,
                Email = collisionEmail,
                EmailConfirmed = true,
                DisplayName = "Email collision",
            })).Succeeded);
            var now = DateTimeOffset.UtcNow;
            db.ScimUserMappings.Add(new ScimUserMapping
            {
                ScimConnectionId = scim.Id,
                UserId = mapped.Id,
                ResourceId = objectId.ToString("D"),
                ExternalId = objectId.ToString("D"),
                UserName = mapped.UserName!,
                UpstreamActive = true,
                ETag = Guid.NewGuid().ToString("N"),
                CreatedAt = now,
                UpdatedAt = now,
                LastSynchronizedAt = now,
            });
            await db.SaveChangesAsync();
        }

        using var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });
        var challenge = await client.GetAsync($"/api/v1/identity/federation/{federation.Id}/challenge");
        var query = QueryHelpers.ParseQuery(challenge.Headers.Location!.Query);
        DynamicFederationTestTransport.Responder = request => request.RequestUri!.AbsolutePath.EndsWith("/token", StringComparison.Ordinal)
            ? JsonResponse(JsonSerializer.Serialize(new { id_token = CreateEntraToken(query["nonce"].ToString(), $"entra-{objectId:N}", collisionEmail, objectId) }))
            : JsonResponse(JsonWebKeySetJson());
        try
        {
            var callback = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(query["state"])}&code=scim-first-code");
            Assert.Equal("/sign-in?mfa=federation", callback.Headers.Location?.OriginalString);

            await using var scope = factory.Services.CreateAsyncScope();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var identity = await db.FederatedIdentities.SingleAsync(item => item.ConnectionId == federation.Id);
            Assert.Equal(mappedUserId, identity.UserId);
            Assert.Equal(objectId, identity.DirectoryObjectId);
            Assert.Equal(1, await db.Users.CountAsync(item => item.Email == collisionEmail));
            Assert.Equal(1, await db.ScimUserMappings.CountAsync(item => item.ScimConnectionId == scim.Id));
            var audit = (await db.AuthorizationAuditEvents
                .Where(item => item.Action == "scim.mapping.oidc-attached")
                .ToArrayAsync()).Single(item => item.AfterJson.Contains(objectId.ToString(), StringComparison.Ordinal));
            Assert.Contains(federation.Id.ToString(), audit.AfterJson, StringComparison.Ordinal);
            Assert.Contains(mappedUserId.ToString(), audit.AfterJson, StringComparison.Ordinal);
            Assert.Contains(Tenant, audit.AfterJson, StringComparison.Ordinal);
            Assert.Contains("oidc-callback", audit.AfterJson, StringComparison.Ordinal);
            Assert.DoesNotContain(collisionEmail, audit.BeforeJson + audit.AfterJson + audit.Details, StringComparison.Ordinal);
        }
        finally
        {
            DynamicFederationTestTransport.Responder = null;
        }
    }

    [Fact]
    public async Task ScimFirstCallbackAttachmentFailureRollsBackIdentityAndAudit()
    {
        var federation = await CreateConnectionAsync(FederationProviderKind.Entra);
        var scim = await CreateScimBindingAsync(federation.Id);
        var objectId = Guid.NewGuid();
        var email = $"entra-attach-failure-{Guid.NewGuid():N}@example.com";
        Guid mappedUserId;
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var mapped = new ApplicationUser
            {
                UserName = $"scim-attach-failure-{Guid.NewGuid():N}",
                Email = $"scim-attach-failure-{Guid.NewGuid():N}@example.com",
                EmailConfirmed = true,
                DisplayName = "SCIM attach failure",
            };
            Assert.True((await users.CreateAsync(mapped)).Succeeded);
            mappedUserId = mapped.Id;
            var now = DateTimeOffset.UtcNow;
            db.ScimUserMappings.Add(new ScimUserMapping
            {
                ScimConnectionId = scim.Id,
                UserId = mapped.Id,
                ResourceId = objectId.ToString("D"),
                ExternalId = objectId.ToString("D"),
                UserName = mapped.UserName!,
                UpstreamActive = true,
                ETag = Guid.NewGuid().ToString("N"),
                CreatedAt = now,
                UpdatedAt = now,
                LastSynchronizedAt = now,
            });
            await db.SaveChangesAsync();
        }

        using var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions { AllowAutoRedirect = false, HandleCookies = true });
        var challenge = await client.GetAsync($"/api/v1/identity/federation/{federation.Id}/challenge");
        var query = QueryHelpers.ParseQuery(challenge.Headers.Location!.Query);
        DynamicFederationTestTransport.Responder = request => request.RequestUri!.AbsolutePath.EndsWith("/token", StringComparison.Ordinal)
            ? JsonResponse(JsonSerializer.Serialize(new { id_token = CreateEntraToken(query["nonce"].ToString(), $"entra-{objectId:N}", email, objectId) }))
            : JsonResponse(JsonWebKeySetJson());
        ScimLifecycleService.MutationFailureInjector = () => new DbUpdateException("injected attach transaction failure");
        try
        {
            var callback = await client.GetAsync($"/api/v1/identity/federation/callback?state={Uri.EscapeDataString(query["state"])}&code=attach-failure-code");
            Assert.Equal("/sign-in?error=federation_sign_in_failed", callback.Headers.Location?.OriginalString);
        }
        finally
        {
            ScimLifecycleService.MutationFailureInjector = null;
            DynamicFederationTestTransport.Responder = null;
        }

        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.False(await verifyDb.FederatedIdentities.AnyAsync(item => item.ConnectionId == federation.Id && item.DirectoryObjectId == objectId));
        Assert.False(await verifyDb.UserLogins.AnyAsync(item => item.UserId == mappedUserId));
        Assert.DoesNotContain((await verifyDb.AuthorizationAuditEvents.Where(item => item.Action == "scim.mapping.oidc-attached").ToArrayAsync()),
            item => item.AfterJson.Contains(objectId.ToString(), StringComparison.Ordinal));
    }

    [Fact]
    public async Task EntraCorrelationRejectsTenantMismatchAndDifferentFederationConnection()
    {
        var federation = await CreateConnectionAsync(FederationProviderKind.Entra);
        var otherFederation = await CreateConnectionAsync(FederationProviderKind.Entra);
        var scim = await CreateScimBindingAsync(federation.Id);
        var objectId = Guid.NewGuid();
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var user = new ApplicationUser
            {
                UserName = $"entra-mismatch-{Guid.NewGuid():N}",
                Email = $"entra-mismatch-{Guid.NewGuid():N}@example.com",
                EmailConfirmed = true,
                DisplayName = "Entra mismatch",
            };
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            Assert.True((await users.CreateAsync(user)).Succeeded);
            db.FederatedIdentities.Add(new FederatedIdentity
            {
                ConnectionId = otherFederation.Id,
                Issuer = EntraIssuer,
                Subject = $"other-{objectId:N}",
                DirectoryTenantId = Tenant,
                DirectoryObjectId = objectId,
                UserId = user.Id,
                CreatedAt = DateTimeOffset.UtcNow,
                UpdatedAt = DateTimeOffset.UtcNow,
            });
            db.FederatedIdentities.Add(new FederatedIdentity
            {
                ConnectionId = federation.Id,
                Issuer = EntraIssuer,
                Subject = $"wrong-tenant-{objectId:N}",
                DirectoryTenantId = "33333333-3333-3333-3333-333333333333",
                DirectoryObjectId = objectId,
                UserId = user.Id,
                CreatedAt = DateTimeOffset.UtcNow,
                UpdatedAt = DateTimeOffset.UtcNow,
            });
            await db.SaveChangesAsync();
        }

        await using var serviceScope = factory.Services.CreateAsyncScope();
        var service = serviceScope.ServiceProvider.GetRequiredService<ScimLifecycleService>();
        Assert.Null(await service.EnsureMappingForOidcFederatedIdentityAsync(scim.Id, objectId.ToString("D"), CancellationToken.None));
    }

    [Fact]
    public async Task EntraScimOwnerMappingIsRejectedBeforeIdentityAttachment()
    {
        var federation = await CreateConnectionAsync(FederationProviderKind.Entra);
        var scim = await CreateScimBindingAsync(federation.Id);
        var objectId = Guid.NewGuid();
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var now = DateTimeOffset.UtcNow;
            db.ScimUserMappings.Add(new ScimUserMapping
            {
                ScimConnectionId = scim.Id,
                UserId = factory.OwnerId,
                ResourceId = objectId.ToString("D"),
                ExternalId = objectId.ToString("D"),
                UserName = IdentityApiFactory.OwnerEmail,
                UpstreamActive = true,
                ETag = Guid.NewGuid().ToString("N"),
                CreatedAt = now,
                UpdatedAt = now,
                LastSynchronizedAt = now,
            });
            await db.SaveChangesAsync();
        }

        await using var serviceScope = factory.Services.CreateAsyncScope();
        var service = serviceScope.ServiceProvider.GetRequiredService<ScimLifecycleService>();
        var result = await service.ResolveEntraMappingForFederationAsync(
            federation.Id, EntraIssuer, Guid.Parse(Tenant), objectId, CancellationToken.None);
        Assert.Equal(ScimEntraMappingResolutionKind.OwnerRejected, result.Kind);
    }

    [Fact]
    public async Task SharedPolicy_RejectsSsrfAndUnknownLengthPayloads()
    {
        Assert.False(FederationEndpointPolicy.IsPublicAddress(IPAddress.Parse("10.0.0.1")));
        Assert.False(FederationEndpointPolicy.IsPublicAddress(IPAddress.Parse("192.0.2.1")));
        Assert.False(FederationEndpointPolicy.IsPublicAddress(IPAddress.Parse("2001:db8::1")));
        await Assert.ThrowsAsync<InvalidDataException>(() => FederationEndpointPolicy.ReadBoundedAsync(
            new StreamContent(new RepeatingStream(FederationEndpointPolicy.MaxResponseBytes + 1)), CancellationToken.None));
    }

    private async Task<FederationConnection> CreateConnectionAsync(FederationProviderKind kind)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var connection = Connection(kind);
        db.FederationConnections.Add(connection);
        await db.SaveChangesAsync();
        return connection;
    }

    private async Task<ScimConnection> CreateScimBindingAsync(Guid federationConnectionId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var now = DateTimeOffset.UtcNow;
        var connection = new ScimConnection
        {
            FederationConnectionId = federationConnectionId,
            Mode = ScimProvisioningMode.Authoritative,
            IsEnabled = true,
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        db.ScimConnections.Add(connection);
        await db.SaveChangesAsync();
        return connection;
    }

    private static FederationConnection Connection(FederationProviderKind kind, string[]? domains = null) => new()
    {
        ProviderKind = kind,
        DisplayName = $"Runtime {Guid.NewGuid():N}",
        Authority = kind == FederationProviderKind.Entra ? EntraIssuer : Issuer,
        ClientId = ClientId,
        ClientSecretReference = SecretReference,
        AllowedDomains = domains ?? [],
        IsEnabled = true,
        JitCreationMode = JitCreationMode.CreateUser,
        ConfigurationVersion = 1,
        ValidationState = FederationConnectionValidationState.Succeeded,
        ValidatedConfigurationVersion = 1,
        ValidatedIssuer = kind == FederationProviderKind.Entra ? EntraIssuer : Issuer,
        ValidatedAuthorizationEndpoint = "https://issuer.runtime.test/authorize",
        ValidatedTokenEndpoint = "https://issuer.runtime.test/token",
        ValidatedJwksUri = "https://issuer.runtime.test/keys",
        ConcurrencyStamp = Guid.NewGuid().ToString("N"),
        CreatedAt = DateTimeOffset.UtcNow,
        UpdatedAt = DateTimeOffset.UtcNow,
    };

    private static ClaimsPrincipal Principal(params (string Type, string Value)[] claims) => new(new ClaimsIdentity(claims.Select(c => new Claim(c.Type, c.Value)), "test"));
    private static string Query(string url, string key) => QueryHelpers.ParseQuery(new Uri(url).Query)[key].ToString();
    private static string Base64Url(byte[] bytes) => Convert.ToBase64String(bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_');
    private static string JsonWebKeySetJson()
    {
        var key = JsonWebKeyConverter.ConvertFromSecurityKey(SigningKey);
        key.Kid = "runtime-key";
        key.Alg = SecurityAlgorithms.RsaSha256;
        key.Use = "sig";
        return JsonSerializer.Serialize(new { keys = new[] { key } });
    }
    private static HttpResponseMessage JsonResponse(string json, string contentType = "application/json") => new(HttpStatusCode.OK) { Content = new StringContent(json, Encoding.UTF8, contentType) };
    private static string CreateToken(string nonce, string subject, string email, IReadOnlyCollection<string>? audiences = null, string? azp = null)
    {
        var claims = new List<Claim> { new("sub", subject), new("email", email), new("email_verified", "true"), new("nonce", nonce), new("name", "Runtime User") };
        if (azp is not null) claims.Add(new Claim("azp", azp));
        return new JwtSecurityTokenHandler().WriteToken(new JwtSecurityToken(Issuer, null, claims.Concat((audiences ?? [ClientId]).Select(a => new Claim(JwtRegisteredClaimNames.Aud, a))), DateTime.UtcNow.AddMinutes(-1), DateTime.UtcNow.AddMinutes(5), new SigningCredentials(SigningKey, SecurityAlgorithms.RsaSha256)));
    }

    private static string CreateEntraToken(string nonce, string subject, string email, Guid objectId)
    {
        var claims = new List<Claim>
        {
            new("sub", subject), new("email", email), new("email_verified", "true"),
            new("nonce", nonce), new("name", "Entra User"), new("tid", Tenant),
            new("oid", objectId.ToString("D")),
        };
        return new JwtSecurityTokenHandler().WriteToken(new JwtSecurityToken(
            EntraIssuer, null, claims.Concat(new[] { new Claim(JwtRegisteredClaimNames.Aud, ClientId) }),
            DateTime.UtcNow.AddMinutes(-1), DateTime.UtcNow.AddMinutes(5),
            new SigningCredentials(SigningKey, SecurityAlgorithms.RsaSha256)));
    }
    private const string Tenant = "11111111-1111-1111-1111-111111111111";
    private const string ObjectId = "22222222-2222-2222-2222-222222222222";
    private const string EntraIssuer = "https://login.microsoftonline.com/11111111-1111-1111-1111-111111111111/v2.0";

    private sealed class RepeatingStream(int length) : Stream
    {
        private int remaining = length;
        public override int Read(byte[] buffer, int offset, int count) { var size = Math.Min(count, remaining); Array.Fill(buffer, (byte)'x', offset, size); remaining -= size; return size; }
        public override ValueTask<int> ReadAsync(Memory<byte> buffer, CancellationToken cancellationToken = default) { var size = Math.Min(buffer.Length, remaining); buffer.Span[..size].Fill((byte)'x'); remaining -= size; return ValueTask.FromResult(size); }
        public override bool CanRead => true; public override bool CanSeek => false; public override bool CanWrite => false; public override long Length => remaining; public override long Position { get; set; }
        public override void Flush() { }
        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException(); public override void SetLength(long value) => throw new NotSupportedException(); public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();
    }
}