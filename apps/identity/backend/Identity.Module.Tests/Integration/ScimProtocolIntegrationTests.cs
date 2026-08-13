global using Vantigo.Identity.Services;

using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Security.Claims;
using System.Text;
using System.Text.Json;

using Microsoft.AspNetCore.Authorization;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

/// <summary>
/// Host-backed protocol coverage. These tests deliberately use the real
/// WebApplicationFactory and PostgreSQL migrations rather than testing DTOs or
/// the protocol service in isolation.
/// </summary>
[Collection(IdentityApiCollection.Name)]
public sealed class ScimProtocolIntegrationTests(IdentityApiFactory factory)
{
    private const string Base = ScimProtocolService.BasePath;

    [Fact]
    public async Task MissingBearerTokenIsRejectedAsScimError() =>
        Assert.Equal(HttpStatusCode.Unauthorized, (await factory.CreateClient().GetAsync(Base + "/Users")).StatusCode);

    [Fact]
    public async Task InvalidBearerTokenIsRejectedAsScimError()
    {
        using var client = CreateClient("not-a-token");
        var response = await client.GetAsync(Base + "/Users");
        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
        Assert.Equal(ScimProtocolService.ScimMediaType, response.Content.Headers.ContentType!.MediaType);
        Assert.Contains("urn:ietf:params:scim:api:messages:2.0:Error", await response.Content.ReadAsStringAsync());
    }

    [Fact]
    public async Task ServiceProviderConfigDeclaresSupportedOperations()
    {
        var setup = await CreateConnectionAsync();
        var response = await setup.Client.GetAsync(Base + "/ServiceProviderConfig");
        var json = await response.Content.ReadAsStringAsync();
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Contains("\"patch\":{\"supported\":true}", json);
        Assert.Contains("\"bulk\":{\"supported\":false", json);
    }

    [Fact]
    public async Task SchemasDeclareUserAndGroupAttributes()
    {
        var setup = await CreateConnectionAsync();
        var json = await setup.Client.GetStringAsync(Base + "/Schemas");
        Assert.Contains(ScimProtocolService.UserSchema, json);
        Assert.Contains(ScimProtocolService.GroupSchema, json);
        Assert.Contains("userName", json);
        Assert.Contains("externalId", json);
        Assert.Contains("members", json);
    }

    [Fact]
    public async Task ResourceTypesDeclareExactEndpoints()
    {
        var setup = await CreateConnectionAsync();
        var json = await setup.Client.GetStringAsync(Base + "/ResourceTypes");
        Assert.Contains(Base + "/Users", json);
        Assert.Contains(Base + "/Groups", json);
    }

    [Fact]
    public async Task UserCreateReadAndApplicationScimJsonWork()
    {
        var setup = await CreateConnectionAsync();
        var response = await SendAsync(setup.Client, HttpMethod.Post, Base + "/Users", User("alice@example.test", "11111111-1111-1111-1111-111111111111"));
        Assert.True(response.StatusCode == HttpStatusCode.Created, await response.Content.ReadAsStringAsync());
        Assert.Equal(ScimProtocolService.ScimMediaType, response.Content.Headers.ContentType!.MediaType);
        var resource = await JsonDocument.ParseAsync(await response.Content.ReadAsStreamAsync());
        var id = resource.RootElement.GetProperty("id").GetString()!;
        Assert.Equal(HttpStatusCode.OK, (await setup.Client.GetAsync(Base + "/Users/" + id)).StatusCode);
    }

    [Fact]
    public async Task UserNameFilterReturnsExactCaseCorrectValue()
    {
        var setup = await CreateConnectionAsync();
        await CreateUserAsync(setup.Client, "case-user@example.test", "22222222-2222-2222-2222-222222222222");
        var response = await setup.Client.GetAsync(Base + "/Users?filter=" + Uri.EscapeDataString("userName eq \"case-user@example.test\""));
        Assert.Equal(1, (await JsonDocument.ParseAsync(await response.Content.ReadAsStreamAsync())).RootElement.GetProperty("totalResults").GetInt32());
    }

    [Fact]
    public async Task ExternalIdFilterUsesConnectionScopedEquality()
    {
        var setup = await CreateConnectionAsync();
        await CreateUserAsync(setup.Client, "external@example.test", "33333333-3333-3333-3333-333333333333");
        var response = await setup.Client.GetAsync(Base + "/Users?filter=" + Uri.EscapeDataString("externalId eq \"33333333-3333-3333-3333-333333333333\""));
        Assert.Equal(1, (await JsonDocument.ParseAsync(await response.Content.ReadAsStreamAsync())).RootElement.GetProperty("totalResults").GetInt32());
    }

    [Fact]
    public async Task AndFilterReturnsEmptyWhenEitherTermDoesNotMatch()
    {
        var setup = await CreateConnectionAsync();
        await CreateUserAsync(setup.Client, "and@example.test", "44444444-4444-4444-4444-444444444444");
        var filter = Uri.EscapeDataString("userName eq \"and@example.test\" and externalId eq \"wrong\"");
        var response = await setup.Client.GetAsync(Base + "/Users?filter=" + filter);
        Assert.Equal(0, (await JsonDocument.ParseAsync(await response.Content.ReadAsStreamAsync())).RootElement.GetProperty("totalResults").GetInt32());
    }

    [Fact]
    public async Task EmptyFilterReturnsEmptyList()
    {
        var setup = await CreateConnectionAsync();
        var document = await JsonDocument.ParseAsync(await (await setup.Client.GetAsync(Base + "/Users")).Content.ReadAsStreamAsync());
        Assert.Equal(0, document.RootElement.GetProperty("totalResults").GetInt32());
    }

    [Fact]
    public async Task PaginationUsesStartIndexAndCount()
    {
        var setup = await CreateConnectionAsync();
        for (var i = 0; i < 3; i++) await CreateUserAsync(setup.Client, $"page-{i}@example.test", Guid.NewGuid().ToString());
        var document = await JsonDocument.ParseAsync(await (await setup.Client.GetAsync(Base + "/Users?startIndex=2&count=1")).Content.ReadAsStreamAsync());
        Assert.Equal(3, document.RootElement.GetProperty("totalResults").GetInt32());
        Assert.Equal(2, document.RootElement.GetProperty("startIndex").GetInt32());
        Assert.Equal(1, document.RootElement.GetProperty("itemsPerPage").GetInt32());
    }

    [Fact]
    public async Task DuplicateExternalIdReturnsConflict()
    {
        var setup = await CreateConnectionAsync();
        Assert.Equal(HttpStatusCode.Created, (await CreateUserAsync(setup.Client, "duplicate-a@example.test", "55555555-5555-5555-5555-555555555555")).StatusCode);
        Assert.Equal(HttpStatusCode.Conflict, (await CreateUserAsync(setup.Client, "duplicate-b@example.test", "55555555-5555-5555-5555-555555555555")).StatusCode);
    }

    [Fact]
    public async Task InvalidContentTypeAndInvalidJsonUseScimErrors()
    {
        var setup = await CreateConnectionAsync();
        using var wrong = new StringContent("{}", Encoding.UTF8, "application/json");
        Assert.Equal(HttpStatusCode.UnsupportedMediaType, (await setup.Client.PostAsync(Base + "/Users", wrong)).StatusCode);
        using var malformed = new StringContent("{", Encoding.UTF8, ScimProtocolService.ScimMediaType);
        Assert.Equal(HttpStatusCode.BadRequest, (await setup.Client.PostAsync(Base + "/Users", malformed)).StatusCode);
    }

    [Fact]
    public async Task BodyIsBoundedBeforeParsingAndUnauthenticatedBodyIsIgnored()
    {
        var setup = await CreateConnectionAsync();
        var before = await JsonDocument.ParseAsync(await (await setup.Client.GetAsync(Base + "/Users")).Content.ReadAsStreamAsync());
        var oversized = new StringContent(new string('x', ScimProtocolService.MaximumRequestBodyBytes + 1), Encoding.UTF8, ScimProtocolService.ScimMediaType);
        var oversizedResponse = await setup.Client.PostAsync(Base + "/Users", oversized);
        await AssertScimErrorAsync(oversizedResponse, HttpStatusCode.RequestEntityTooLarge, "tooLarge", "256 KiB");

        using var unauthenticated = factory.CreateClient();
        using var request = new HttpRequestMessage(HttpMethod.Post, Base + "/Users")
        {
            Content = new StringContent("{", Encoding.UTF8, ScimProtocolService.ScimMediaType),
        };
        var missingTokenResponse = await unauthenticated.SendAsync(request);
        await AssertScimErrorAsync(missingTokenResponse, HttpStatusCode.Unauthorized, "invalidValue", "bearer token");
        var after = await JsonDocument.ParseAsync(await (await setup.Client.GetAsync(Base + "/Users")).Content.ReadAsStreamAsync());
        Assert.Equal(before.RootElement.GetProperty("totalResults").GetInt32(), after.RootElement.GetProperty("totalResults").GetInt32());
    }

    [Fact]
    public async Task RateLimitReturnsScim429ErrorShape()
    {
        var setup = await CreateConnectionAsync();
        HttpResponseMessage? response = null;
        for (var i = 0; i < 121; i++)
            response = await setup.Client.GetAsync(Base + "/Users");
        await AssertScimErrorAsync(response!, HttpStatusCode.TooManyRequests, "tooMany", "rate limit");
    }

    [Fact]
    public async Task PatchAcceptsCaseInsensitiveOperationAndPath()
    {
        var setup = await CreateConnectionAsync();
        var create = await CreateUserAsync(setup.Client, "patch@example.test", "66666666-6666-6666-6666-666666666666");
        var id = await IdAsync(create);
        var patch = new { schemas = new[] { ScimProtocolService.PatchSchema }, Operations = new[] { new { op = "RePlAcE", path = "AcTiVe", value = false } } };
        var response = await SendAsync(setup.Client, HttpMethod.Patch, Base + "/Users/" + id, patch);
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Contains("\"active\":false", await response.Content.ReadAsStringAsync());
    }

    [Fact]
    public async Task PatchRejectsUnknownOperationAtomically()
    {
        var setup = await CreateConnectionAsync();
        var create = await CreateUserAsync(setup.Client, "atomic@example.test", "77777777-7777-7777-7777-777777777777");
        var id = await IdAsync(create);
        var patch = new { schemas = new[] { ScimProtocolService.PatchSchema }, Operations = new object[] { new { op = "replace", path = "displayName", value = "changed" }, new { op = "move", path = "unknown", value = "bad" } } };
        Assert.Equal(HttpStatusCode.BadRequest, (await SendAsync(setup.Client, HttpMethod.Patch, Base + "/Users/" + id, patch)).StatusCode);
        Assert.DoesNotContain("changed", await (await setup.Client.GetAsync(Base + "/Users/" + id)).Content.ReadAsStringAsync());
    }

    [Fact]
    public async Task InactiveUserRemainsRetrievable()
    {
        var setup = await CreateConnectionAsync();
        var create = await CreateUserAsync(setup.Client, "inactive@example.test", "88888888-8888-8888-8888-888888888888");
        var id = await IdAsync(create);
        await SendAsync(setup.Client, HttpMethod.Patch, Base + "/Users/" + id, new { schemas = new[] { ScimProtocolService.PatchSchema }, Operations = new[] { new { op = "replace", path = "active", value = false } } });
        var read = await setup.Client.GetAsync(Base + "/Users/" + id);
        Assert.Equal(HttpStatusCode.OK, read.StatusCode);
        Assert.Contains("\"active\":false", await read.Content.ReadAsStringAsync());
    }

    [Fact]
    public async Task DeleteTombstonesUserAndPreservesMapping()
    {
        var setup = await CreateConnectionAsync();
        var create = await CreateUserAsync(setup.Client, "delete@example.test", "99999999-9999-9999-9999-999999999999");
        var id = await IdAsync(create);
        Assert.Equal(HttpStatusCode.NoContent, (await setup.Client.DeleteAsync(Base + "/Users/" + id)).StatusCode);
        Assert.Contains("\"active\":false", await (await setup.Client.GetAsync(Base + "/Users/" + id)).Content.ReadAsStringAsync());
        await using var scope = factory.Services.CreateAsyncScope();
        Assert.True(await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().ScimUserMappings.AnyAsync(item => item.ResourceId == id));
    }

    [Fact]
    public async Task StaleIfMatchReturnsPreconditionFailure()
    {
        var setup = await CreateConnectionAsync();
        var create = await CreateUserAsync(setup.Client, "etag@example.test", Guid.NewGuid().ToString());
        var id = await IdAsync(create);
        Assert.NotNull(create.Headers.ETag);
        using var request = new HttpRequestMessage(HttpMethod.Patch, Base + "/Users/" + id);
        request.Headers.IfMatch.Add(new EntityTagHeaderValue("\"stale\""));
        request.Content = JsonContent(new { schemas = new[] { ScimProtocolService.PatchSchema }, Operations = new[] { new { op = "replace", path = "active", value = false } } });
        var response = await setup.Client.SendAsync(request);
        await AssertScimErrorAsync(response, HttpStatusCode.PreconditionFailed, "invalidVers", "stale");
    }

    [Fact]
    public async Task EntraRejectsMalformedAndImmutableExternalIdsWithScimDetails()
    {
        var setup = await CreateConnectionAsync();
        var malformed = await CreateUserAsync(setup.Client, "entra-malformed@example.test", "not-an-object-id");
        await AssertScimErrorAsync(malformed, HttpStatusCode.BadRequest, "invalidValue", "object-id GUID");

        var externalId = Guid.NewGuid().ToString();
        var created = await CreateUserAsync(setup.Client, "entra-immutable@example.test", externalId);
        var id = await IdAsync(created);
        var invalidPatch = new
        {
            schemas = new[] { ScimProtocolService.PatchSchema },
            Operations = new[] { new { op = "replace", path = "externalId", value = "not-a-guid" } },
        };
        await AssertScimErrorAsync(await SendAsync(setup.Client, HttpMethod.Patch, Base + "/Users/" + id, invalidPatch),
            HttpStatusCode.BadRequest, "invalidValue", "object-id GUID");

        var immutablePatch = new
        {
            schemas = new[] { ScimProtocolService.PatchSchema },
            Operations = new[] { new { op = "replace", path = "externalId", value = Guid.NewGuid().ToString() } },
        };
        await AssertScimErrorAsync(await SendAsync(setup.Client, HttpMethod.Patch, Base + "/Users/" + id, immutablePatch),
            HttpStatusCode.Conflict, "mutability", "immutable");
    }

    [Fact]
    public async Task SecondConnectionCannotReadFirstResource()
    {
        var first = await CreateConnectionAsync();
        var second = await CreateConnectionAsync();
        var id = await IdAsync(await CreateUserAsync(first.Client, "isolated@example.test", Guid.NewGuid().ToString()));
        Assert.Equal(HttpStatusCode.NotFound, (await second.Client.GetAsync(Base + "/Users/" + id)).StatusCode);
    }

    [Fact]
    public async Task GroupCreateQueryAndExcludedMembersWork()
    {
        var setup = await CreateConnectionAsync();
        var user = await CreateUserAsync(setup.Client, "group-member@example.test", Guid.NewGuid().ToString());
        var userId = await IdAsync(user);
        var group = await SendAsync(setup.Client, HttpMethod.Post, Base + "/Groups", new { schemas = new[] { ScimProtocolService.GroupSchema }, displayName = "Engineering", externalId = "eng", members = new[] { new { value = userId, type = "User" } } });
        Assert.Equal(HttpStatusCode.Created, group.StatusCode);
        var groupId = await IdAsync(group);
        Assert.DoesNotContain("members", await (await setup.Client.GetAsync(Base + "/Groups/" + groupId + "?excludedAttributes=members")).Content.ReadAsStringAsync());
        var groups = await setup.Client.GetAsync(Base + "/Groups?filter=" + Uri.EscapeDataString("displayName eq \"Engineering\""));
        Assert.Equal(1, (await JsonDocument.ParseAsync(await groups.Content.ReadAsStreamAsync())).RootElement.GetProperty("totalResults").GetInt32());
    }

    [Fact]
    public async Task GroupMembershipAddAndRemoveAreIdempotent()
    {
        var setup = await CreateConnectionAsync();
        var userId = await IdAsync(await CreateUserAsync(setup.Client, "member@example.test", Guid.NewGuid().ToString()));
        var groupId = await IdAsync(await SendAsync(setup.Client, HttpMethod.Post, Base + "/Groups", new { schemas = new[] { ScimProtocolService.GroupSchema }, displayName = "Idempotent", externalId = Guid.NewGuid().ToString() }));
        var add = new { schemas = new[] { ScimProtocolService.PatchSchema }, Operations = new[] { new { op = "add", path = "members", value = new[] { new { value = userId } } } } };
        Assert.Equal(HttpStatusCode.OK, (await SendAsync(setup.Client, HttpMethod.Patch, Base + "/Groups/" + groupId, add)).StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await SendAsync(setup.Client, HttpMethod.Patch, Base + "/Groups/" + groupId, add)).StatusCode);
        var remove = new { schemas = new[] { ScimProtocolService.PatchSchema }, Operations = new[] { new { op = "remove", path = "members[value eq \"" + userId + "\"]" } } };
        Assert.Equal(HttpStatusCode.OK, (await SendAsync(setup.Client, HttpMethod.Patch, Base + "/Groups/" + groupId, remove)).StatusCode);
    }

    [Fact]
    public async Task GroupReplaceIsFullReplacementAndRemoveAllRevokesDerivedAccess()
    {
        var setup = await CreateConnectionAsync();
        var firstId = await IdAsync(await CreateUserAsync(setup.Client, "group-first@example.test", Guid.NewGuid().ToString()));
        var secondId = await IdAsync(await CreateUserAsync(setup.Client, "group-second@example.test", Guid.NewGuid().ToString()));
        var firstUserId = await UserIdForResourceAsync(setup.ConnectionId, firstId);
        var secondUserId = await UserIdForResourceAsync(setup.ConnectionId, secondId);
        var groupResponse = await SendAsync(setup.Client, HttpMethod.Post, Base + "/Groups", new
        {
            schemas = new[] { ScimProtocolService.GroupSchema },
            displayName = "Full replacement group",
            externalId = Guid.NewGuid().ToString(),
            members = new[] { new { value = firstId }, new { value = secondId } },
        });
        Assert.Equal(HttpStatusCode.Created, groupResponse.StatusCode);
        var groupJson = await JsonDocument.ParseAsync(await groupResponse.Content.ReadAsStreamAsync());
        var groupId = groupJson.RootElement.GetProperty("id").GetString()!;

        using var owner = await factory.CreateOwnerClientAsync();
        var roleResponse = await owner.PostAsJsonAsync("/api/v1/identity/access/roles", new
        {
            name = "scim-protocol-group-role-" + Guid.NewGuid().ToString("N"),
            displayName = "SCIM protocol group role",
            description = "SCIM protocol test role",
            permissionKeys = new[] { "customers:view" },
        });
        Assert.Equal(HttpStatusCode.Created, roleResponse.StatusCode);
        var role = await JsonDocument.ParseAsync(await roleResponse.Content.ReadAsStreamAsync());
        var roleId = role.RootElement.GetProperty("id").GetGuid();
        var groupVersion = groupJson.RootElement.GetProperty("meta").GetProperty("version").GetString()!;
        var mapped = await owner.PostAsJsonAsync($"/api/v1/identity/access/groups/{groupId}/role-mappings/{roleId}", new
        {
            concurrencyStamp = groupVersion,
            scimConnectionId = setup.ConnectionId,
        });
        Assert.Equal(HttpStatusCode.OK, mapped.StatusCode);
        Assert.True(await AuthorizePermissionAsync(firstUserId, "customers:view"));
        Assert.True(await AuthorizePermissionAsync(secondUserId, "customers:view"));
        var currentGroup = await setup.Client.GetAsync(Base + "/Groups/" + groupId);
        Assert.Equal(HttpStatusCode.OK, currentGroup.StatusCode);

        var replace = new
        {
            schemas = new[] { ScimProtocolService.PatchSchema },
            Operations = new[] { new { op = "replace", path = "members", value = new[] { new { value = firstId } } } },
        };
        var replaceRequest = new HttpRequestMessage(HttpMethod.Patch, Base + "/Groups/" + groupId)
        {
            Content = JsonContent(replace),
        };
        replaceRequest.Headers.IfMatch.Add(currentGroup.Headers.ETag!);
        var replaced = await setup.Client.SendAsync(replaceRequest);
        Assert.Equal(HttpStatusCode.OK, replaced.StatusCode);
        Assert.False(await AuthorizePermissionAsync(secondUserId, "customers:view"));
        Assert.True(await AuthorizePermissionAsync(firstUserId, "customers:view"));

        var removeAll = new
        {
            schemas = new[] { ScimProtocolService.PatchSchema },
            Operations = new[] { new { op = "remove", path = "members" } },
        };
        var removeRequest = new HttpRequestMessage(HttpMethod.Patch, Base + "/Groups/" + groupId)
        {
            Content = JsonContent(removeAll),
        };
        removeRequest.Headers.IfMatch.Add(replaced.Headers.ETag!);
        var removed = await setup.Client.SendAsync(removeRequest);
        Assert.Equal(HttpStatusCode.OK, removed.StatusCode);
        var final = await JsonDocument.ParseAsync(await (await setup.Client.GetAsync(Base + "/Groups/" + groupId)).Content.ReadAsStreamAsync());
        Assert.Empty(final.RootElement.GetProperty("members").EnumerateArray());
        Assert.False(await AuthorizePermissionAsync(firstUserId, "customers:view"));
        Assert.False(await AuthorizePermissionAsync(secondUserId, "customers:view"));

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.All(await db.AccessGroupMemberships.Where(item => item.GroupId == Guid.Parse(groupId)).ToArrayAsync(),
            membership => Assert.False(membership.IsUpstreamPresent));
    }

    [Fact]
    public async Task UnknownGroupMemberIsRejected()
    {
        var setup = await CreateConnectionAsync();
        var response = await SendAsync(setup.Client, HttpMethod.Post, Base + "/Groups", new { schemas = new[] { ScimProtocolService.GroupSchema }, displayName = "Bad member", members = new[] { new { value = Guid.NewGuid().ToString() } } });
        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Fact]
    public async Task GroupDeleteTombstonesAndRemovesDerivedAccess()
    {
        var setup = await CreateConnectionAsync();
        var group = await SendAsync(setup.Client, HttpMethod.Post, Base + "/Groups", new { schemas = new[] { ScimProtocolService.GroupSchema }, displayName = "Tombstone group" });
        var id = await IdAsync(group);
        Assert.Equal(HttpStatusCode.NoContent, (await setup.Client.DeleteAsync(Base + "/Groups/" + id)).StatusCode);
        Assert.Contains("\"active\":false", await (await setup.Client.GetAsync(Base + "/Groups/" + id)).Content.ReadAsStringAsync());
    }

    [Fact]
    public async Task DisabledConnectionRejectsPreviouslyValidToken()
    {
        var setup = await CreateConnectionAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var connection = await db.ScimConnections.SingleAsync(item => item.Id == setup.ConnectionId);
        connection.IsEnabled = false;
        await db.SaveChangesAsync();
        Assert.Equal(HttpStatusCode.Unauthorized, (await setup.Client.GetAsync(Base + "/Users")).StatusCode);
    }

    [Fact]
    public async Task RevokedConnectionRejectsPreviouslyValidToken()
    {
        var setup = await CreateConnectionAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var token = await db.ScimBearerTokens.SingleAsync(item => item.ScimConnectionId == setup.ConnectionId);
        token.RevokedAt = DateTimeOffset.UtcNow;
        await db.SaveChangesAsync();
        Assert.Equal(HttpStatusCode.Unauthorized, (await setup.Client.GetAsync(Base + "/Users")).StatusCode);
    }

    [Fact]
    public async Task OverlapTokenBehaviorRemainsConnectionScoped()
    {
        var setup = await CreateConnectionAsync();
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var old = await scope.ServiceProvider.GetRequiredService<ScimTokenService>().CreateAsync(setup.ConnectionId, 2, DateTimeOffset.UtcNow, CancellationToken.None);
        var verifier = scope.ServiceProvider.GetRequiredService<ScimTokenService>();
        Assert.True((await verifier.VerifyAsync(old.Plaintext, CancellationToken.None)).Succeeded);
    }

    [Fact]
    public async Task AuditDoesNotContainBearerOrPayloadSecrets()
    {
        var setup = await CreateConnectionAsync();
        var secret = Guid.NewGuid().ToString("N");
        await CreateUserAsync(setup.Client, "audit@example.test", secret);
        await using var scope = factory.Services.CreateAsyncScope();
        var events = await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().AuthorizationAuditEvents.AsNoTracking().OrderByDescending(item => item.Id).Take(5).ToListAsync();
        Assert.DoesNotContain(events, item => (item.BeforeJson + item.AfterJson).Contains(setup.Token, StringComparison.Ordinal) || (item.BeforeJson + item.AfterJson).Contains(secret, StringComparison.Ordinal));
    }

    [Fact]
    public async Task ScimAuditFactsRedactUsernameDisplayEmailAndProfileValues()
    {
        var setup = await CreateConnectionAsync();
        var userName = "audit-user-" + Guid.NewGuid().ToString("N") + "@example.test";
        var displayName = "Private display " + Guid.NewGuid().ToString("N");
        var givenName = "PrivateGiven" + Guid.NewGuid().ToString("N");
        var familyName = "PrivateFamily" + Guid.NewGuid().ToString("N");
        var email = "private-email-" + Guid.NewGuid().ToString("N") + "@example.test";
        var response = await SendAsync(setup.Client, HttpMethod.Post, Base + "/Users", new
        {
            schemas = new[] { ScimProtocolService.UserSchema },
            userName,
            externalId = Guid.NewGuid().ToString(),
            displayName,
            name = new { givenName, familyName },
            emails = new[] { new { value = email, type = "work", primary = true } },
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var resourceId = await IdAsync(response);

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var audit = await db.AuthorizationAuditEvents.AsNoTracking()
            .Where(item => item.Action == "scim.user.created" && item.AfterJson.Contains(resourceId))
            .OrderByDescending(item => item.Id)
            .FirstAsync();
        var json = audit.BeforeJson + audit.AfterJson + audit.Details;
        Assert.DoesNotContain(userName, json, StringComparison.Ordinal);
        Assert.DoesNotContain(displayName, json, StringComparison.Ordinal);
        Assert.DoesNotContain(email, json, StringComparison.Ordinal);
        Assert.DoesNotContain(givenName, json, StringComparison.Ordinal);
        Assert.DoesNotContain(familyName, json, StringComparison.Ordinal);
    }

    [Fact]
    public async Task OidcFirstCorrelationAndScimResourceRollbackWhenProtocolAuditFails()
    {
        var setup = await CreateConnectionAsync();
        var objectId = Guid.NewGuid();
        var userId = Guid.Empty;
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<Microsoft.AspNetCore.Identity.UserManager<ApplicationUser>>();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var user = new ApplicationUser
            {
                UserName = "oidc-rollback-" + Guid.NewGuid().ToString("N"),
                Email = "oidc-rollback-" + Guid.NewGuid().ToString("N") + "@example.test",
                EmailConfirmed = true,
                DisplayName = "Before protocol mutation",
            };
            Assert.True((await users.CreateAsync(user)).Succeeded);
            userId = user.Id;
            db.FederatedIdentities.Add(new FederatedIdentity
            {
                ConnectionId = await FederationIdAsync(setup.ConnectionId),
                Issuer = await FederationIssuerAsync(setup.ConnectionId),
                Subject = "oidc-rollback-subject-" + objectId.ToString("N"),
                DirectoryTenantId = "00000000-0000-0000-0000-000000000000",
                DirectoryObjectId = objectId,
                UserId = user.Id,
                CreatedAt = DateTimeOffset.UtcNow,
                UpdatedAt = DateTimeOffset.UtcNow,
            });
            await db.SaveChangesAsync();
        }

        ScimProtocolService.AuditFailureInjector = () => new InvalidOperationException("injected protocol audit failure");
        try
        {
            var response = await CreateUserAsync(setup.Client, "oidc-protocol-rollback@example.test", objectId.ToString("D"));
            Assert.Equal(HttpStatusCode.InternalServerError, response.StatusCode);
        }
        finally
        {
            ScimProtocolService.AuditFailureInjector = null;
        }

        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.False(await verifyDb.ScimUserMappings.AnyAsync(item => item.ScimConnectionId == setup.ConnectionId && item.UserId == userId));
        Assert.DoesNotContain(await verifyDb.AuthorizationAuditEvents.AsNoTracking().Where(item => item.Action.StartsWith("scim.")).ToArrayAsync(),
            item => item.AfterJson.Contains(objectId.ToString(), StringComparison.Ordinal));
        Assert.Equal("Before protocol mutation", await verifyDb.Users.Where(item => item.Id == userId).Select(item => item.DisplayName).SingleAsync());
    }

    [Fact]
    public async Task NoEmailLinkingAllowsDistinctExternalIdentityToFailRatherThanLink()
    {
        var setup = await CreateConnectionAsync();
        Assert.Equal(HttpStatusCode.Created, (await CreateUserAsync(setup.Client, "same@example.test", Guid.NewGuid().ToString())).StatusCode);
        Assert.NotEqual(HttpStatusCode.Created, (await CreateUserAsync(setup.Client, "same@example.test", Guid.NewGuid().ToString())).StatusCode);
    }

    [Fact]
    public async Task BulkEndpointIsNotExposed()
    {
        var setup = await CreateConnectionAsync();
        Assert.Equal(HttpStatusCode.NotFound, (await setup.Client.GetAsync(Base + "/Bulk")).StatusCode);
    }

    private HttpClient CreateClient(string token)
    {
        var client = factory.CreateClient();
        client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", token);
        return client;
    }

    private async Task<(Guid ConnectionId, string Token, HttpClient Client)> CreateConnectionAsync()
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var now = DateTimeOffset.UtcNow;
        var federation = new FederationConnection
        {
            ProviderKind = FederationProviderKind.Entra,
            DisplayName = "SCIM protocol federation " + Guid.NewGuid().ToString("N"),
            Authority = "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0",
            ClientId = "scim-test",
            IsEnabled = true,
            ValidatedIssuer = "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0",
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        var connection = new ScimConnection { FederationConnectionId = federation.Id, IsEnabled = true, ConcurrencyStamp = Guid.NewGuid().ToString("N"), CreatedAt = now, UpdatedAt = now };
        db.FederationConnections.Add(federation);
        db.ScimConnections.Add(connection);
        await db.SaveChangesAsync();
        var created = await scope.ServiceProvider.GetRequiredService<ScimTokenService>().CreateAsync(connection.Id, connection.TokenVersion, now, CancellationToken.None);
        return (connection.Id, created.Plaintext, CreateClient(created.Plaintext));
    }

    private async Task<Guid> FederationIdAsync(Guid scimConnectionId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        return await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().ScimConnections
            .Where(item => item.Id == scimConnectionId)
            .Select(item => item.FederationConnectionId)
            .SingleAsync();
    }

    private async Task<Guid> UserIdForResourceAsync(Guid scimConnectionId, string resourceId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        return await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().ScimUserMappings
            .Where(item => item.ScimConnectionId == scimConnectionId && item.ResourceId == resourceId)
            .Select(item => item.UserId)
            .SingleAsync();
    }

    private async Task<string> FederationIssuerAsync(Guid scimConnectionId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var federationId = await db.ScimConnections.Where(item => item.Id == scimConnectionId)
            .Select(item => item.FederationConnectionId).SingleAsync();
        return await db.FederationConnections.Where(item => item.Id == federationId)
            .Select(item => item.ValidatedIssuer!).SingleAsync();
    }

    private async Task<bool> AuthorizePermissionAsync(Guid userId, string permission)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var authorization = scope.ServiceProvider.GetRequiredService<IAuthorizationService>();
        var principal = new ClaimsPrincipal(new ClaimsIdentity(
            [new Claim(ClaimTypes.NameIdentifier, userId.ToString())], "test"));
        return (await authorization.AuthorizeAsync(principal, null, $"permission:{permission}")).Succeeded;
    }

    private async Task<HttpResponseMessage> CreateUserAsync(HttpClient client, string userName, string externalId) =>
        await SendAsync(client, HttpMethod.Post, Base + "/Users", User(userName, externalId));

    private static object User(string userName, string externalId) => new
    {
        schemas = new[] { ScimProtocolService.UserSchema },
        userName,
        externalId,
        active = true,
        displayName = userName,
        emails = new[] { new { value = userName, type = "work", primary = true } },
    };

    private static async Task<string> IdAsync(HttpResponseMessage response)
    {
        var document = await JsonDocument.ParseAsync(await response.Content.ReadAsStreamAsync());
        return document.RootElement.GetProperty("id").GetString()!;
    }

    private static async Task<HttpResponseMessage> SendAsync(HttpClient client, HttpMethod method, string uri, object body)
    {
        using var request = new HttpRequestMessage(method, uri) { Content = JsonContent(body) };
        return await client.SendAsync(request);
    }

    private static StringContent JsonContent(object body) => new(JsonSerializer.Serialize(body), Encoding.UTF8, ScimProtocolService.ScimMediaType);

    private static async Task AssertScimErrorAsync(HttpResponseMessage response, HttpStatusCode status, string scimType, string detailPart)
    {
        Assert.Equal(status, response.StatusCode);
        Assert.Equal(ScimProtocolService.ScimMediaType, response.Content.Headers.ContentType!.MediaType);
        using var document = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
        Assert.Equal(scimType, document.RootElement.GetProperty("scimType").GetString());
        Assert.Contains(detailPart, document.RootElement.GetProperty("detail").GetString(), StringComparison.OrdinalIgnoreCase);
        Assert.Equal(((int)status).ToString(), document.RootElement.GetProperty("status").GetString());
    }
}