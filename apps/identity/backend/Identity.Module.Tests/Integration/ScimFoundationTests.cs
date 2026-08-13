using System.Net;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

using Microsoft.AspNetCore.Http;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Endpoints.Auth;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class ScimFoundationTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task ScimControlPlaneIsOwnerOnly()
    {
        using var anonymous = factory.CreateCookieClient();
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await anonymous.GetAsync("/api/v1/identity/access/scim")).StatusCode);

        var credentials = await factory.CreateUserWithCredentialsAsync("ScimStandard");
        using var user = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await user.GetAsync("/api/v1/identity/access/scim")).StatusCode);
    }

    [Fact]
    public async Task ScimCreatePersistsOnlyPepperedHashAndListNeverReturnsPlaintext()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var federationId = await CreateFederationAsync();
        var create = await owner.PostAsJsonAsync("/api/v1/identity/access/scim", new
        {
            federationConnectionId = federationId,
            mode = "Authoritative",
        });
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var body = (await create.Content.ReadFromJsonAsync<ScimCreateResponse>())!;
        Assert.False(string.IsNullOrWhiteSpace(body.Token));
        Assert.Equal("Authoritative", body.Mode);

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var token = await db.ScimBearerTokens.SingleAsync(item => item.ScimConnectionId == body.Id);
        Assert.NotEqual(body.Token, token.TokenHash);
        Assert.Equal(64, token.TokenHash.Length);
        Assert.DoesNotContain(body.Token, token.TokenHash, StringComparison.Ordinal);

        var list = await owner.GetAsync("/api/v1/identity/access/scim");
        var listJson = await list.Content.ReadAsStringAsync();
        Assert.DoesNotContain(body.Token, listJson, StringComparison.Ordinal);
        Assert.DoesNotContain("\"token\"", listJson, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("\"mode\":\"Authoritative\"", listJson, StringComparison.Ordinal);

        var audit = await db.AuthorizationAuditEvents.AsNoTracking()
            .Where(item => item.Action == "scim.connection.created")
            .OrderByDescending(item => item.Id)
            .FirstAsync();
        Assert.DoesNotContain(body.Token, audit.BeforeJson + audit.AfterJson, StringComparison.Ordinal);
    }

    [Fact]
    public async Task DuplicateFederationBindingReturnsConflict()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var federationId = await CreateFederationAsync();
        var request = new { federationConnectionId = federationId, mode = "Additive" };
        Assert.Equal(HttpStatusCode.Created,
            (await owner.PostAsJsonAsync("/api/v1/identity/access/scim", request)).StatusCode);
        Assert.Equal(HttpStatusCode.Conflict,
            (await owner.PostAsJsonAsync("/api/v1/identity/access/scim", request)).StatusCode);
    }

    [Fact]
    public async Task ScimModeContractAcceptsNamedValuesCaseInsensitivelyAndRejectsNumericValues()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var authoritativeFederation = await CreateFederationAsync();
        var additiveFederation = await CreateFederationAsync();
        var numericFederation = await CreateFederationAsync();

        var authoritative = await owner.PostAsJsonAsync("/api/v1/identity/access/scim", new
        {
            federationConnectionId = authoritativeFederation,
            mode = "aUtHoRiTaTiVe",
        });
        Assert.Equal(HttpStatusCode.Created, authoritative.StatusCode);
        Assert.Equal("Authoritative", (await authoritative.Content.ReadFromJsonAsync<ScimCreateResponse>())!.Mode);

        var additive = await owner.PostAsJsonAsync("/api/v1/identity/access/scim", new
        {
            federationConnectionId = additiveFederation,
            mode = "aDdItIvE",
        });
        Assert.Equal(HttpStatusCode.Created, additive.StatusCode);
        Assert.Equal("Additive", (await additive.Content.ReadFromJsonAsync<ScimCreateResponse>())!.Mode);

        var numeric = await owner.PostAsJsonAsync("/api/v1/identity/access/scim", new
        {
            federationConnectionId = numericFederation,
            mode = 0,
        });
        Assert.Equal(HttpStatusCode.BadRequest, numeric.StatusCode);
    }

    [Fact]
    public async Task DeletingFederationOrScimConnectionReturnsConflictAndPreservesProvenance()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var federationId = await CreateFederationAsync();
        var create = await owner.PostAsJsonAsync("/api/v1/identity/access/scim", new
        {
            federationConnectionId = federationId,
            mode = "Authoritative",
        });
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var scim = (await create.Content.ReadFromJsonAsync<ScimCreateResponse>())!;
        var userId = await factory.CreateUserAsync("ScimProvenanceUser");
        var groupId = Guid.NewGuid();
        var mappingId = Guid.NewGuid();

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var now = DateTimeOffset.UtcNow;
            db.ScimUserMappings.Add(new ScimUserMapping
            {
                Id = mappingId,
                ScimConnectionId = scim.Id,
                UserId = userId,
                ResourceId = Guid.NewGuid().ToString("N"),
                ExternalId = Guid.NewGuid().ToString("N"),
                UserName = "provenance@example.test",
                UpstreamActive = true,
                ETag = Guid.NewGuid().ToString("N"),
                CreatedAt = now,
                UpdatedAt = now,
                LastSynchronizedAt = now,
            });
            db.AccessGroups.Add(new AccessGroup
            {
                Id = groupId,
                ScimConnectionId = scim.Id,
                DisplayName = $"Provenance group {Guid.NewGuid():N}",
                Source = AccessGroupSource.Scim,
                ExternalId = Guid.NewGuid().ToString("N"),
                IsActive = true,
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
                CreatedAt = now,
                UpdatedAt = now,
            });
            await db.SaveChangesAsync();
        }

        string federationStamp;
        string scimStamp;
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            federationStamp = (await db.FederationConnections.SingleAsync(item => item.Id == federationId)).ConcurrencyStamp;
            scimStamp = (await db.ScimConnections.SingleAsync(item => item.Id == scim.Id)).ConcurrencyStamp;
        }

        var federationDelete = await owner.SendAsync(new HttpRequestMessage(
            HttpMethod.Delete, $"/api/v1/identity/access/federation-connections/{federationId}")
        {
            Content = JsonContent.Create(new { concurrencyStamp = federationStamp }),
        });
        Assert.Equal(HttpStatusCode.Conflict, federationDelete.StatusCode);

        var scimDelete = await owner.SendAsync(new HttpRequestMessage(
            HttpMethod.Delete, $"/api/v1/identity/access/scim/{scim.Id}")
        {
            Content = JsonContent.Create(new { concurrencyStamp = scimStamp }),
        });
        Assert.Equal(HttpStatusCode.Conflict, scimDelete.StatusCode);

        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.True(await verifyDb.FederationConnections.AnyAsync(item => item.Id == federationId));
        Assert.True(await verifyDb.ScimConnections.AnyAsync(item => item.Id == scim.Id));
        Assert.True(await verifyDb.ScimBearerTokens.AnyAsync(item => item.ScimConnectionId == scim.Id));
        Assert.True(await verifyDb.ScimUserMappings.AnyAsync(item => item.Id == mappingId));
        Assert.True(await verifyDb.AccessGroups.AnyAsync(item => item.Id == groupId));
    }

    [Fact]
    public async Task EnableUsesExactConcurrencyStampAndStaleRequestReturnsConflict()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var created = await CreateScimAsync(owner);
        var stale = await owner.PostAsJsonAsync($"/api/v1/identity/access/scim/{created.Id}/enable", new
        {
            concurrencyStamp = "stale-stamp",
        });
        Assert.Equal(HttpStatusCode.Conflict, stale.StatusCode);

        var enabled = await owner.PostAsJsonAsync($"/api/v1/identity/access/scim/{created.Id}/enable", new
        {
            concurrencyStamp = created.ConcurrencyStamp,
        });
        Assert.Equal(HttpStatusCode.OK, enabled.StatusCode);
        var enabledBody = (await enabled.Content.ReadFromJsonAsync<ScimConnectionResponse>())!;
        Assert.True(enabledBody.IsEnabled);
        Assert.NotEqual(created.ConcurrencyStamp, enabledBody.ConcurrencyStamp);
    }

    [Fact]
    public async Task RotateReturnsTokenOnceAndRetainsOnlyImmediatePreviousOverlap()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var created = await CreateScimAsync(owner);
        var firstToken = created.Token;
        var rotated = await owner.PostAsJsonAsync($"/api/v1/identity/access/scim/{created.Id}/rotate", new
        {
            concurrencyStamp = created.ConcurrencyStamp,
        });
        Assert.Equal(HttpStatusCode.OK, rotated.StatusCode);
        var second = (await rotated.Content.ReadFromJsonAsync<ScimRotateResponse>())!;
        Assert.NotEqual(firstToken, second.Token);

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var tokens = await db.ScimBearerTokens.AsNoTracking()
            .Where(item => item.ScimConnectionId == created.Id)
            .OrderBy(item => item.Version)
            .ToArrayAsync();
        Assert.Equal(2, tokens.Length);
        Assert.False(tokens[0].IsCurrent);
        Assert.Null(tokens[0].RevokedAt);
        Assert.NotNull(tokens[0].ExpiresAt);
        Assert.True(tokens[1].IsCurrent);
        Assert.Null(tokens[1].RevokedAt);
        var list = await owner.GetAsync("/api/v1/identity/access/scim");
        Assert.DoesNotContain(firstToken, await list.Content.ReadAsStringAsync(), StringComparison.Ordinal);
    }

    [Fact]
    public async Task RevokeRequiresCurrentStampRevokesAllTokensAndDisablesConnection()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var created = await CreateScimAsync(owner);
        var rotated = await owner.PostAsJsonAsync($"/api/v1/identity/access/scim/{created.Id}/rotate", new
        {
            concurrencyStamp = created.ConcurrencyStamp,
        });
        var rotatedBody = (await rotated.Content.ReadFromJsonAsync<ScimRotateResponse>())!;

        var revoke = await owner.PostAsJsonAsync($"/api/v1/identity/access/scim/{created.Id}/revoke", new
        {
            concurrencyStamp = rotatedBody.ConcurrencyStamp,
        });
        Assert.Equal(HttpStatusCode.NoContent, revoke.StatusCode);

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var connection = await db.ScimConnections.SingleAsync(item => item.Id == created.Id);
        Assert.False(connection.IsEnabled);
        Assert.NotEqual(rotatedBody.ConcurrencyStamp, connection.ConcurrencyStamp);
        Assert.All(await db.ScimBearerTokens.Where(item => item.ScimConnectionId == created.Id).ToArrayAsync(), token =>
        {
            Assert.NotNull(token.RevokedAt);
            Assert.False(token.IsCurrent);
        });

        Assert.Equal(HttpStatusCode.Conflict,
            (await owner.PostAsJsonAsync($"/api/v1/identity/access/scim/{created.Id}/enable", new
            {
                concurrencyStamp = rotatedBody.ConcurrencyStamp,
            })).StatusCode);
    }

    [Fact]
    public async Task VerifierAcceptsCurrentAndImmediateOverlapOnly()
    {
        var setup = await CreateDirectConnectionAsync(isEnabled: true);
        var clock = new MutableTimeProvider(DateTimeOffset.Parse("2026-01-01T00:00:00Z"));
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var verifier = new ScimTokenService(db, new FixedPepper("test-pepper"), clock);
        var old = await verifier.CreateAsync(setup.Id, 1, clock.GetUtcNow().AddMinutes(-1), CancellationToken.None);
        old.Token.IsCurrent = false;
        old.Token.ExpiresAt = clock.GetUtcNow().AddMinutes(10);
        var current = await verifier.CreateAsync(setup.Id, 2, clock.GetUtcNow(), CancellationToken.None);
        await db.SaveChangesAsync();

        Assert.True((await verifier.VerifyAsync(current.Plaintext, CancellationToken.None)).Succeeded);
        Assert.True((await verifier.VerifyAsync(old.Plaintext, CancellationToken.None)).Succeeded);
        Assert.False((await verifier.VerifyAsync("wrong-token", CancellationToken.None)).Succeeded);
    }

    [Fact]
    public async Task VerifierRejectsExpiredAndRevokedOverlap()
    {
        var setup = await CreateDirectConnectionAsync(isEnabled: true);
        var clock = new MutableTimeProvider(DateTimeOffset.Parse("2026-01-01T00:00:00Z"));
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var verifier = new ScimTokenService(db, new FixedPepper("test-pepper"), clock);
        var expired = await verifier.CreateAsync(setup.Id, 1, clock.GetUtcNow().AddMinutes(-2), CancellationToken.None);
        expired.Token.IsCurrent = false;
        expired.Token.ExpiresAt = clock.GetUtcNow().AddMinutes(-1);
        var revoked = await verifier.CreateAsync(setup.Id, 2, clock.GetUtcNow(), CancellationToken.None);
        revoked.Token.IsCurrent = false;
        revoked.Token.ExpiresAt = clock.GetUtcNow().AddMinutes(10);
        revoked.Token.RevokedAt = clock.GetUtcNow();
        await db.SaveChangesAsync();

        Assert.False((await verifier.VerifyAsync(expired.Plaintext, CancellationToken.None)).Succeeded);
        Assert.False((await verifier.VerifyAsync(revoked.Plaintext, CancellationToken.None)).Succeeded);
    }

    [Fact]
    public async Task VerifierRejectsAnyTokenForDisabledConnection()
    {
        var setup = await CreateDirectConnectionAsync(isEnabled: false);
        var clock = new MutableTimeProvider(DateTimeOffset.Parse("2026-01-01T00:00:00Z"));
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var verifier = new ScimTokenService(db, new FixedPepper("test-pepper"), clock);
        var token = await verifier.CreateAsync(setup.Id, 1, clock.GetUtcNow(), CancellationToken.None);
        Assert.False((await verifier.VerifyAsync(token.Plaintext, CancellationToken.None)).Succeeded);
    }

    [Fact]
    public async Task OverrideNormalizesReasonAndAuditsBeforeAndAfterFields()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var connection = await CreateDirectConnectionAsync(isEnabled: true);
        var userId = await factory.CreateUserAsync("ScimOverrideUser");
        var mapping = await CreateMappingAsync(connection.Id, userId, upstreamActive: false);

        var response = await owner.PutAsJsonAsync($"/api/v1/identity/access/scim/{connection.Id}/users/{userId}/override", new
        {
            @override = 0,
            reason = "  emergency   exception   approved  ",
            etag = mapping.ETag,
        });
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var saved = await db.ScimUserMappings.SingleAsync(item => item.Id == mapping.Id);
        Assert.Equal("emergency exception approved", saved.LifecycleOverrideReason);
        var audit = await db.AuthorizationAuditEvents.AsNoTracking()
            .Where(item => item.Action == "scim.user.lifecycle-override")
            .OrderByDescending(item => item.Id)
            .FirstAsync();
        Assert.Contains("LifecycleOverrideReason", audit.BeforeJson, StringComparison.Ordinal);
        Assert.Contains("emergency exception approved", audit.AfterJson, StringComparison.Ordinal);
        Assert.DoesNotContain("ReasonProvided", audit.BeforeJson + audit.AfterJson, StringComparison.Ordinal);
    }

    [Fact]
    public async Task OverrideRejectsBlankControlAndOversizedReasons()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var connection = await CreateDirectConnectionAsync(isEnabled: true);
        var userId = await factory.CreateUserAsync("ScimReasonUser");
        var mapping = await CreateMappingAsync(connection.Id, userId, upstreamActive: true);

        foreach (var reason in new[] { "  \t ", "bad\u0001reason", new string('x', 501) })
        {
            var response = await owner.PutAsJsonAsync($"/api/v1/identity/access/scim/{connection.Id}/users/{userId}/override", new
            {
                @override = 1,
                reason,
                etag = mapping.ETag,
            });
            Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        }
    }

    [Fact]
    public async Task OwnerCannotReceiveLifecycleOverride()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var connection = await CreateDirectConnectionAsync(isEnabled: true);
        var mapping = await CreateMappingAsync(connection.Id, factory.OwnerId, upstreamActive: true);
        var response = await owner.PutAsJsonAsync($"/api/v1/identity/access/scim/{connection.Id}/users/{factory.OwnerId}/override", new
        {
            @override = 1,
            reason = "must not disable owner",
            etag = mapping.ETag,
        });
        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Fact]
    public async Task OverrideRequiresExactMappingETag()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var connection = await CreateDirectConnectionAsync(isEnabled: true);
        var userId = await factory.CreateUserAsync("ScimEtagUser");
        var mapping = await CreateMappingAsync(connection.Id, userId, upstreamActive: true);
        var response = await owner.PutAsJsonAsync($"/api/v1/identity/access/scim/{connection.Id}/users/{userId}/override", new
        {
            @override = 1,
            reason = "stale mapping",
            etag = "stale-etag",
        });
        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [Fact]
    public async Task ConcurrentContextsWithTheSameMappingETagAllowOnlyOneCommit()
    {
        var connection = await CreateDirectConnectionAsync(isEnabled: true);
        var userId = await factory.CreateUserAsync("ScimConcurrentEtagUser");
        var mapping = await CreateMappingAsync(connection.Id, userId, upstreamActive: true);

        var ready = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        var waiting = 0;
        ScimControlPlaneService.OverrideBeforeSaveAsync = _ =>
        {
            if (Interlocked.Increment(ref waiting) == 2) ready.TrySetResult(true);
            return ready.Task;
        };
        try
        {
            await using var firstScope = factory.Services.CreateAsyncScope();
            await using var secondScope = factory.Services.CreateAsyncScope();
            var firstService = firstScope.ServiceProvider.GetRequiredService<ScimControlPlaneService>();
            var secondService = secondScope.ServiceProvider.GetRequiredService<ScimControlPlaneService>();
            firstScope.ServiceProvider.GetRequiredService<IHttpContextAccessor>().HttpContext = new DefaultHttpContext();
            secondScope.ServiceProvider.GetRequiredService<IHttpContextAccessor>().HttpContext = new DefaultHttpContext();
            var results = await Task.WhenAll(
                firstService.SetOverrideAsync(connection.Id, userId,
                    new ScimOverrideRequest(ScimLifecycleOverride.ForceDisable, "first", mapping.ETag), CancellationToken.None),
                secondService.SetOverrideAsync(connection.Id, userId,
                    new ScimOverrideRequest(ScimLifecycleOverride.ForceEnable, "second", mapping.ETag), CancellationToken.None));
            var statuses = results.Select(result => (result as Microsoft.AspNetCore.Http.IStatusCodeHttpResult)?.StatusCode)
                .OrderBy(status => status).ToArray();
            Assert.Equal([(int)HttpStatusCode.OK, (int)HttpStatusCode.Conflict], statuses);
        }
        finally
        {
            ScimControlPlaneService.OverrideBeforeSaveAsync = null;
        }

        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var saved = await verifyDb.ScimUserMappings.SingleAsync(item => item.Id == mapping.Id);
        Assert.True(saved.LifecycleOverride is ScimLifecycleOverride.ForceDisable or ScimLifecycleOverride.ForceEnable);
        Assert.NotEqual(mapping.ETag, saved.ETag);
    }

    [Fact]
    public async Task LifecycleEffectiveRulesCoverAuthoritativeAdditiveAndOverrides()
    {
        var authoritative = await CreateDirectConnectionAsync(ScimProvisioningMode.Authoritative, isEnabled: true);
        var additive = await CreateDirectConnectionAsync(ScimProvisioningMode.Additive, isEnabled: true);
        var authoritativeUser = await factory.CreateUserAsync("ScimAuthoritativeUser");
        var additiveUser = await factory.CreateUserAsync("ScimAdditiveUser");
        var forceEnableUser = await factory.CreateUserAsync("ScimForceEnableUser");
        var forceDisableUser = await factory.CreateUserAsync("ScimForceDisableUser");
        await CreateMappingAsync(authoritative.Id, authoritativeUser, upstreamActive: false);
        await CreateMappingAsync(additive.Id, additiveUser, upstreamActive: false);
        var enabledMapping = await CreateMappingAsync(authoritative.Id, forceEnableUser, upstreamActive: false);
        var disabledMapping = await CreateMappingAsync(additive.Id, forceDisableUser, upstreamActive: true);
        await SetMappingOverrideAsync(enabledMapping.Id, ScimLifecycleOverride.ForceEnable, "manual enable");
        await SetMappingOverrideAsync(disabledMapping.Id, ScimLifecycleOverride.ForceDisable, "manual disable");

        await using var scope = factory.Services.CreateAsyncScope();
        var lifecycle = scope.ServiceProvider.GetRequiredService<ScimLifecycleService>();
        Assert.True(await lifecycle.IsEffectivelyDisabledAsync(authoritativeUser, CancellationToken.None));
        Assert.False(await lifecycle.IsEffectivelyDisabledAsync(additiveUser, CancellationToken.None));
        Assert.False(await lifecycle.IsEffectivelyDisabledAsync(forceEnableUser, CancellationToken.None));
        Assert.True(await lifecycle.IsEffectivelyDisabledAsync(forceDisableUser, CancellationToken.None));
    }

    [Fact]
    public async Task DisabledEffectiveSessionIsRejectedByCookieValidator()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync("ScimSessionUser");
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        var connection = await CreateDirectConnectionAsync(ScimProvisioningMode.Authoritative, isEnabled: true);
        await CreateMappingAsync(connection.Id, credentials.Id, upstreamActive: false);

        var session = await client.GetAsync("/api/v1/identity/session");
        Assert.Equal(HttpStatusCode.Unauthorized, session.StatusCode);
    }

    [Fact]
    public async Task FactoryUsesTheReferencedEnvironmentPepperBeforeHostInitialization()
    {
        Assert.Equal("integration-scim-token-pepper",
            Environment.GetEnvironmentVariable(IdentityApiFactory.ScimTokenPepperReference));
        await using var scope = factory.Services.CreateAsyncScope();
        var pepper = scope.ServiceProvider.GetRequiredService<IScimTokenPepper>();
        Assert.Equal(Encoding.UTF8.GetBytes("integration-scim-token-pepper"), pepper.GetPepper());
    }

    private async Task<ScimCreateResponse> CreateScimAsync(HttpClient owner,
        ScimProvisioningMode mode = ScimProvisioningMode.Authoritative)
    {
        var federationId = await CreateFederationAsync();
        var response = await owner.PostAsJsonAsync("/api/v1/identity/access/scim", new
        {
            federationConnectionId = federationId,
            mode = mode.ToString(),
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return (await response.Content.ReadFromJsonAsync<ScimCreateResponse>())!;
    }

    private async Task<Guid> CreateFederationAsync()
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var now = DateTimeOffset.UtcNow;
        var federation = new FederationConnection
        {
            ProviderKind = FederationProviderKind.Generic,
            DisplayName = $"Scim federation {Guid.NewGuid():N}",
            Authority = "https://issuer.scim.integration.test",
            ClientId = "scim-client",
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        db.FederationConnections.Add(federation);
        await db.SaveChangesAsync();
        return federation.Id;
    }

    private async Task<ScimConnection> CreateDirectConnectionAsync(
        ScimProvisioningMode mode = ScimProvisioningMode.Authoritative,
        bool isEnabled = true)
    {
        var federationId = await CreateFederationAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var now = DateTimeOffset.UtcNow;
        var connection = new ScimConnection
        {
            FederationConnectionId = federationId,
            Mode = mode,
            IsEnabled = isEnabled,
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        db.ScimConnections.Add(connection);
        await db.SaveChangesAsync();
        return connection;
    }

    private async Task<ScimUserMapping> CreateMappingAsync(Guid connectionId, Guid userId, bool upstreamActive)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var now = DateTimeOffset.UtcNow;
        var mapping = new ScimUserMapping
        {
            ScimConnectionId = connectionId,
            UserId = userId,
            ResourceId = Guid.NewGuid().ToString("N"),
            ExternalId = Guid.NewGuid().ToString("N"),
            UserName = $"scim-{Guid.NewGuid():N}@integration.test",
            UpstreamActive = upstreamActive,
            ETag = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
            LastSynchronizedAt = now,
        };
        db.ScimUserMappings.Add(mapping);
        await db.SaveChangesAsync();
        return mapping;
    }

    private async Task SetMappingOverrideAsync(Guid mappingId, ScimLifecycleOverride lifecycleOverride, string reason)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var mapping = await db.ScimUserMappings.SingleAsync(item => item.Id == mappingId);
        mapping.LifecycleOverride = lifecycleOverride;
        mapping.LifecycleOverrideReason = reason;
        mapping.ETag = Guid.NewGuid().ToString("N");
        await db.SaveChangesAsync();
    }

    private sealed record ScimCreateResponse(
        Guid Id,
        string Mode,
        string Token,
        int TokenVersion,
        string ConcurrencyStamp);

    private sealed record ScimRotateResponse(
        Guid Id,
        string Token,
        int TokenVersion,
        string ConcurrencyStamp);

    private sealed record ScimConnectionResponse(
        Guid Id,
        Guid FederationConnectionId,
        string Mode,
        bool IsEnabled,
        int TokenVersion,
        string ConcurrencyStamp);

}

internal sealed class FixedPepper(string value) : IScimTokenPepper
{
    public byte[] GetPepper() => Encoding.UTF8.GetBytes(value);
}

internal sealed class MutableTimeProvider(DateTimeOffset current) : TimeProvider
{
    private DateTimeOffset current = current;
    public override DateTimeOffset GetUtcNow() => current;
    public void SetUtcNow(DateTimeOffset value) => current = value;
}