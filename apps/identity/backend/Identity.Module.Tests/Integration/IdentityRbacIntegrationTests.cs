using System.Net;
using System.Net.Http.Json;
using System.Security.Claims;
using System.Text.Json;

using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentityRbacIntegrationTests(IdentityApiFactory factory)
{
    private const string Permission = "identity:manage";
    private const string PermissionA = "customers:view";
    private const string PermissionB = "customers:create";

    [Fact]
    public async Task FreshBootstrapCreatesNormalizedProtectedOwnerAndUserRoles()
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var roles = await db.Roles.AsNoTracking().Where(role => role.Name == AuthRoles.Owner || role.Name == AuthRoles.User)
            .ToListAsync();
        var metadata = await db.RoleMetadata.AsNoTracking().Where(item => roles.Select(role => role.Id).Contains(item.RoleId))
            .ToDictionaryAsync(item => item.RoleId);

        Assert.Equal(2, roles.Count);
        Assert.All(roles, role => Assert.Equal(role.Name!.ToUpperInvariant(), role.NormalizedName));
        Assert.All(roles, role =>
        {
            Assert.True(metadata[role.Id].IsSystem);
            Assert.True(metadata[role.Id].IsBuiltIn);
        });
        var ownerRole = roles.Single(role => role.Name == AuthRoles.Owner);
        Assert.Equal(factory.OwnerId, await db.UserRoles.Where(assignment => assignment.RoleId == ownerRole.Id)
            .Select(assignment => assignment.UserId).SingleAsync());
    }

    [Fact]
    public async Task OwnerIsImplicitlyAllowedAndUserIsDeniedUntilAnAdditiveRoleIsAssigned()
    {
        var user = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);

        Assert.True(await AuthorizePermissionAsync(factory.OwnerId, ownerClaim: true));
        Assert.False(await AuthorizePermissionAsync(user.Id));

        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"rbac-additive-{Guid.NewGuid():N}", [Permission]);
        var assignment = await AssignRoleAsync(owner, user.Id, role.Id);
        Assert.Equal(HttpStatusCode.OK, assignment.StatusCode);
        Assert.True(await AuthorizePermissionAsync(user.Id));

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var roleNames = await db.UserRoles.Where(item => item.UserId == user.Id).Join(db.Roles,
            assignmentItem => assignmentItem.RoleId, roleItem => roleItem.Id, (_, roleItem) => roleItem.Name).ToListAsync();
        Assert.Contains(AuthRoles.User, roleNames);
        Assert.Contains(role.Name, roleNames);
    }

    [Fact]
    public async Task PermissionReplacementAndAssignmentRevocationApplyImmediately()
    {
        var user = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"rbac-revoke-{Guid.NewGuid():N}", [Permission]);
        await EnsureSuccess(await AssignRoleAsync(owner, user.Id, role.Id));
        Assert.True(await AuthorizePermissionAsync(user.Id));

        var version = await RoleVersionAsync(role.Id);
        var replace = await owner.PutAsJsonAsync($"/api/v1/identity/access/roles/{role.Id}", new
        {
            name = role.Name,
            displayName = role.Name,
            description = "Revoked",
            permissionKeys = Array.Empty<string>(),
            concurrencyStamp = version,
        });
        Assert.Equal(HttpStatusCode.OK, replace.StatusCode);
        Assert.False(await AuthorizePermissionAsync(user.Id));

        var userVersion = await UserVersionAsync(user.Id);
        var removeAssignment = await owner.PutAsJsonAsync($"/api/v1/identity/access/users/{user.Id}/roles", new
        {
            roleIds = Array.Empty<Guid>(),
            concurrencyStamp = userVersion,
        });
        Assert.Equal(HttpStatusCode.OK, removeAssignment.StatusCode);
        Assert.False(await AuthorizePermissionAsync(user.Id));
        await AssertUserStillHasSystemRoleAsync(user.Id, AuthRoles.User);
    }

    [Fact]
    public async Task ScimGroupRoleAuthorizationHonorsForceOverridesAndPreservesDirectRoles()
    {
        var user = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var derivedRole = await CreateRoleAsync(owner, $"scim-derived-{Guid.NewGuid():N}", [PermissionA]);
        var directRole = await CreateRoleAsync(owner, $"scim-direct-{Guid.NewGuid():N}", [PermissionB]);
        await EnsureSuccess(await AssignRoleAsync(owner, user.Id, directRole.Id));

        var now = DateTimeOffset.UtcNow;
        var federation = new FederationConnection
        {
            ProviderKind = FederationProviderKind.Generic,
            DisplayName = $"SCIM authorization {Guid.NewGuid():N}",
            Authority = $"https://scim-authorization-{Guid.NewGuid():N}.integration.test",
            ClientId = "scim-authorization-client",
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        var connection = new ScimConnection
        {
            FederationConnectionId = federation.Id,
            IsEnabled = true,
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        var group = new AccessGroup
        {
            ScimConnectionId = connection.Id,
            DisplayName = $"SCIM authorization group {Guid.NewGuid():N}",
            Source = AccessGroupSource.Scim,
            ExternalId = Guid.NewGuid().ToString("N"),
            IsActive = true,
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            db.FederationConnections.Add(federation);
            db.ScimConnections.Add(connection);
            db.AccessGroups.Add(group);
            db.AccessGroupMemberships.Add(new AccessGroupMembership
            {
                GroupId = group.Id,
                UserId = user.Id,
                Source = AccessGroupSource.Scim,
                IsUpstreamPresent = true,
                UpdatedAt = now,
            });
            db.AccessGroupRoleMappings.Add(new AccessGroupRoleMapping
            {
                GroupId = group.Id,
                RoleId = derivedRole.Id,
                Source = AccessGroupSource.Scim,
                CreatedAt = now,
            });
            await db.SaveChangesAsync();
        }

        Assert.True(await AuthorizePermissionAsync(user.Id, PermissionA));
        Assert.True(await AuthorizePermissionAsync(user.Id, PermissionB));
        await SetOverrideAsync(group.Id, user.Id, AccessGroupMembershipOverride.ForceNonMember, upstreamPresent: true);
        Assert.False(await AuthorizePermissionAsync(user.Id, PermissionA));
        Assert.True(await AuthorizePermissionAsync(user.Id, PermissionB));
        await SetOverrideAsync(group.Id, user.Id, AccessGroupMembershipOverride.ForceMember, upstreamPresent: false);
        Assert.True(await AuthorizePermissionAsync(user.Id, PermissionA));
        Assert.True(await AuthorizePermissionAsync(user.Id, PermissionB));
        await SetGroupActiveAsync(group.Id, false);
        Assert.False(await AuthorizePermissionAsync(user.Id, PermissionA));
        Assert.True(await AuthorizePermissionAsync(user.Id, PermissionB));
    }

    [Fact]
    public async Task AssignmentPreservesProtectedSystemRolesAndReturnsARevision()
    {
        var user = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"rbac-system-preserve-{Guid.NewGuid():N}", []);
        var response = await AssignRoleAsync(owner, user.Id, role.Id);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var body = await response.Content.ReadFromJsonAsync<AssignmentResponse>();
        Assert.NotNull(body);
        Assert.NotEmpty(body!.Version);
        await AssertUserStillHasSystemRoleAsync(user.Id, AuthRoles.User);
        Assert.Contains(role.Id, body.RoleIds);

        var second = await owner.PutAsJsonAsync($"/api/v1/identity/access/users/{user.Id}/roles", new
        {
            roleIds = new[] { role.Id },
            concurrencyStamp = body.Version,
        });
        Assert.Equal(HttpStatusCode.OK, second.StatusCode);
    }

    [Fact]
    public async Task RoleMutationsRequireCurrentVersionAndReturnAReplacementVersion()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"rbac-version-{Guid.NewGuid():N}", []);
        var stale = await owner.PutAsJsonAsync($"/api/v1/identity/access/roles/{role.Id}", new
        {
            name = role.Name,
            displayName = role.Name,
            description = "stale",
            permissionKeys = Array.Empty<string>(),
            concurrencyStamp = "stale-version",
        });
        Assert.Equal(HttpStatusCode.Conflict, stale.StatusCode);

        var current = await RoleVersionAsync(role.Id);
        var replacement = await owner.PutAsJsonAsync($"/api/v1/identity/access/roles/{role.Id}", new
        {
            name = role.Name,
            displayName = role.Name,
            description = "current",
            permissionKeys = Array.Empty<string>(),
            concurrencyStamp = current,
        });
        Assert.Equal(HttpStatusCode.OK, replacement.StatusCode);
        var result = await replacement.Content.ReadFromJsonAsync<RoleResponse>();
        Assert.NotNull(result);
        Assert.NotEqual(current, result!.Version);
    }

    [Fact]
    public async Task AuthorizationMutationConflictSurfacesAreDeterministicAcrossRoleAssignmentAndDelegationApis()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"rbac-conflict-map-{Guid.NewGuid():N}", []);

        var duplicate = await owner.PostAsJsonAsync("/api/v1/identity/access/roles", new
        {
            name = role.Name,
            displayName = role.Name,
            description = "duplicate",
            permissionKeys = Array.Empty<string>(),
        });
        Assert.Equal(HttpStatusCode.Conflict, duplicate.StatusCode);

        var target = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var assignmentConflict = await owner.PutAsJsonAsync($"/api/v1/identity/access/users/{target.Id}/roles", new
        {
            roleIds = new[] { role.Id },
            concurrencyStamp = "stale-user-version",
        });
        Assert.Equal(HttpStatusCode.Conflict, assignmentConflict.StatusCode);

        var delegation = await CreateDelegationAsync(owner, target.Id, DateTimeOffset.UtcNow.AddHours(1));
        var delegationConflict = await owner.PutAsJsonAsync($"/api/v1/identity/access/delegations/{delegation.Id}", new
        {
            granteeUserId = target.Id,
            expiresAt = DateTimeOffset.UtcNow.AddHours(2),
            permissionKeys = Array.Empty<string>(),
            stewardedRoleIds = Array.Empty<Guid>(),
            canCreateRoles = true,
            concurrencyStamp = "stale-delegation-version",
        });
        Assert.Equal(HttpStatusCode.Conflict, delegationConflict.StatusCode);

        using var deleteRequest = new HttpRequestMessage(
            HttpMethod.Delete, new Uri($"/api/v1/identity/access/roles/{role.Id}", UriKind.Relative))
        {
            Content = JsonContent.Create(new { concurrencyStamp = "stale-role-version" }),
        };
        Assert.Equal(HttpStatusCode.Conflict, (await owner.SendAsync(deleteRequest)).StatusCode);
    }

    [Fact]
    public async Task SystemRoleMutationAndDeletionAreRejected()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var systemRole = await RoleInfoAsync(AuthRoles.User);
        var update = await owner.PutAsJsonAsync($"/api/v1/identity/access/roles/{systemRole.Id}", new
        {
            name = AuthRoles.User + "-tampered",
            displayName = "Tampered",
            description = "Tampered",
            permissionKeys = Array.Empty<string>(),
            concurrencyStamp = systemRole.Version,
        });
        Assert.Equal(HttpStatusCode.Conflict, update.StatusCode);

        using var deleteRequest = new HttpRequestMessage(
            HttpMethod.Delete, new Uri($"/api/v1/identity/access/roles/{systemRole.Id}", UriKind.Relative))
        {
            Content = JsonContent.Create(new { concurrencyStamp = systemRole.Version }),
        };
        var delete = await owner.SendAsync(deleteRequest);
        Assert.Equal(HttpStatusCode.Conflict, delete.StatusCode);
        Assert.Equal(AuthRoles.User, (await RoleInfoAsync(AuthRoles.User)).Name);
    }

    [Fact]
    public async Task NonOwnerAndAnonymousCannotUseManagementSurface()
    {
        using var anonymous = factory.CreateCookieClient();
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await anonymous.GetAsync("/api/v1/identity/access/roles")).StatusCode);

        var user = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var standard = await factory.CreateAuthenticatedClientAsync(user.Email, user.Password);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await standard.GetAsync("/api/v1/identity/access/roles")).StatusCode);
    }

    [Fact]
    public async Task AccessMeAndUserDirectoryExposeSafeManagementProjections()
    {
        var ordinary = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var ordinaryClient = await factory.CreateAuthenticatedClientAsync(ordinary.Email, ordinary.Password);
        var ordinaryMe = await ordinaryClient.GetFromJsonAsync<JsonElement>("/api/v1/identity/access/me");
        Assert.False(ordinaryMe.GetProperty("canManageAuthorization").GetBoolean());
        Assert.Equal(JsonValueKind.Null, ordinaryMe.GetProperty("administrationScope").ValueKind);
        Assert.False(ordinaryMe.TryGetProperty("delegations", out _));
        Assert.Equal(HttpStatusCode.Forbidden,
            (await ordinaryClient.GetAsync("/api/v1/identity/access/users")).StatusCode);

        using var owner = await factory.CreateOwnerClientAsync();
        var ownerMe = await owner.GetFromJsonAsync<JsonElement>("/api/v1/identity/access/me");
        var ownerAdministrationScope = ownerMe.GetProperty("administrationScope");
        Assert.True(ownerAdministrationScope.GetProperty("isOwner").GetBoolean());
        Assert.Empty(ownerAdministrationScope.GetProperty("delegationScopes").EnumerateArray());
        Assert.Equal(["delegationScopes", "isOwner"], ownerAdministrationScope.EnumerateObject()
            .Select(property => property.Name).OrderBy(name => name).ToArray());
        Assert.False(ownerAdministrationScope.TryGetProperty("canCreateRoles", out _));
        Assert.False(ownerAdministrationScope.TryGetProperty("grantablePermissionKeys", out _));
        Assert.False(ownerAdministrationScope.TryGetProperty("stewardedRoleIds", out _));
        var users = await owner.GetFromJsonAsync<JsonElement[]>("/api/v1/identity/access/users");
        Assert.NotNull(users);
        Assert.Contains(users!, item => item.GetProperty("id").GetGuid() == ordinary.Id);
        Assert.All(users!, item =>
        {
            Assert.True(item.TryGetProperty("roles", out _));
            Assert.False(item.TryGetProperty("passwordHash", out _));
            Assert.False(item.TryGetProperty("securityStamp", out _));
            Assert.False(item.TryGetProperty("audit", out _));
        });
    }

    [Fact]
    public async Task ActiveDelegateGetsManagementMarkerAndDirectoryExcludesProtectedTargets()
    {
        var delegateUser = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var ordinary = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        await CreateDelegationAsync(owner, delegateUser.Id, DateTimeOffset.UtcNow.AddHours(1));

        using var delegated = await factory.CreateAuthenticatedClientAsync(delegateUser.Email, delegateUser.Password);
        var me = await delegated.GetFromJsonAsync<JsonElement>("/api/v1/identity/access/me");
        Assert.True(me.GetProperty("canManageAuthorization").GetBoolean());
        var administrationScope = me.GetProperty("administrationScope");
        Assert.False(administrationScope.GetProperty("isOwner").GetBoolean());
        var delegationScope = Assert.Single(administrationScope.GetProperty("delegationScopes").EnumerateArray());
        Assert.Equal(["id", "canCreateRoles", "grantablePermissionKeys", "stewardedRoleIds", "assignableRoleIds"],
            delegationScope.EnumerateObject().Select(property => property.Name).ToArray());
        Assert.False(administrationScope.TryGetProperty("canCreateRoles", out _));
        Assert.False(administrationScope.TryGetProperty("grantablePermissionKeys", out _));
        Assert.False(administrationScope.TryGetProperty("stewardedRoleIds", out _));
        var directory = await delegated.GetFromJsonAsync<JsonElement[]>("/api/v1/identity/access/users");
        Assert.NotNull(directory);
        Assert.Contains(directory!, item => item.GetProperty("id").GetGuid() == ordinary.Id);
        Assert.DoesNotContain(directory!, item => item.GetProperty("id").GetGuid() == factory.OwnerId);
        Assert.DoesNotContain(directory!, item => item.GetProperty("id").GetGuid() == delegateUser.Id);
    }

    [Fact]
    public async Task DelegateEmptyAssignmentPreservesOutOfBoundaryRoleButOwnerCanRemoveIt()
    {
        var delegateUser = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var target = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var outsideRole = await CreateRoleAsync(owner, $"rbac-outside-{Guid.NewGuid():N}", []);
        var managedRole = await CreateRoleAsync(owner, $"rbac-managed-{Guid.NewGuid():N}", []);
        var initialAssignment = await owner.PutAsJsonAsync($"/api/v1/identity/access/users/{target.Id}/roles", new
        {
            roleIds = new[] { outsideRole.Id, managedRole.Id },
            concurrencyStamp = await UserVersionAsync(target.Id),
        });
        await EnsureSuccess(initialAssignment);
        var delegation = await CreateDelegationAsync(owner, delegateUser.Id, DateTimeOffset.UtcNow.AddHours(1), [managedRole.Id]);
        await CreateDelegationAsync(owner, delegateUser.Id, DateTimeOffset.UtcNow.AddHours(1), [outsideRole.Id]);

        using var delegated = await factory.CreateAuthenticatedClientAsync(delegateUser.Email, delegateUser.Password);
        var response = await delegated.PutAsJsonAsync($"/api/v1/identity/access/users/{target.Id}/roles", new
        {
            roleIds = new[] { managedRole.Id },
            delegationId = delegation.Id,
            concurrencyStamp = await UserVersionAsync(target.Id),
        });
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        await AssertUserHasRoleAsync(target.Id, outsideRole.Id);
        await AssertUserHasRoleAsync(target.Id, managedRole.Id);
        await AssertUserStillHasSystemRoleAsync(target.Id, AuthRoles.User);
        var targetAccess = await owner.GetFromJsonAsync<JsonElement>($"/api/v1/identity/access/users/{target.Id}");
        Assert.Contains(managedRole.Id, targetAccess.GetProperty("roleIds").EnumerateArray().Select(item => item.GetGuid()));

        var ownerResponse = await owner.PutAsJsonAsync($"/api/v1/identity/access/users/{target.Id}/roles", new
        {
            roleIds = Array.Empty<Guid>(),
            concurrencyStamp = await UserVersionAsync(target.Id),
        });
        Assert.Equal(HttpStatusCode.OK, ownerResponse.StatusCode);
        await AssertUserLacksRoleAsync(target.Id, outsideRole.Id);
        await AssertUserLacksRoleAsync(target.Id, managedRole.Id);
        await AssertUserStillHasSystemRoleAsync(target.Id, AuthRoles.User);
    }

    [Fact]
    public async Task DisjointDelegationScopesCannotSynthesizeCombinedRoleAuthority()
    {
        var delegateUser = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var target = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var combinedRole = await CreateRoleAsync(owner, $"rbac-combined-{Guid.NewGuid():N}", [PermissionA, PermissionB]);
        var scopeA = await CreateDelegationAsync(owner, delegateUser.Id, DateTimeOffset.UtcNow.AddHours(1),
            [combinedRole.Id], [PermissionA]);
        var scopeB = await CreateDelegationAsync(owner, delegateUser.Id, DateTimeOffset.UtcNow.AddHours(1),
            [combinedRole.Id], [PermissionB]);
        using var delegated = await factory.CreateAuthenticatedClientAsync(delegateUser.Email, delegateUser.Password);
        var me = await delegated.GetFromJsonAsync<JsonElement>("/api/v1/identity/access/me");
        Assert.True(me.GetProperty("canManageAuthorization").GetBoolean());
        var scopes = me.GetProperty("administrationScope").GetProperty("delegationScopes").EnumerateArray().ToArray();
        Assert.Equal(2, scopes.Length);
        Assert.Contains(scopes, scope => scope.GetProperty("id").GetGuid() == scopeA.Id &&
            scope.GetProperty("grantablePermissionKeys").EnumerateArray().Select(item => item.GetString()).SequenceEqual([PermissionA]) &&
            !scope.GetProperty("assignableRoleIds").EnumerateArray().Any());
        Assert.Contains(scopes, scope => scope.GetProperty("id").GetGuid() == scopeB.Id &&
            scope.GetProperty("grantablePermissionKeys").EnumerateArray().Select(item => item.GetString()).SequenceEqual([PermissionB]) &&
            !scope.GetProperty("assignableRoleIds").EnumerateArray().Any());
        Assert.False(me.GetProperty("administrationScope").TryGetProperty("grantablePermissionKeys", out _));
        var assignment = await delegated.PutAsJsonAsync($"/api/v1/identity/access/users/{target.Id}/roles", new
        {
            roleIds = new[] { combinedRole.Id },
            concurrencyStamp = await UserVersionAsync(target.Id),
        });
        Assert.Equal(HttpStatusCode.Forbidden, assignment.StatusCode);

        assignment = await delegated.PutAsJsonAsync($"/api/v1/identity/access/users/{target.Id}/roles", new
        {
            roleIds = new[] { combinedRole.Id },
            delegationId = scopeA.Id,
            concurrencyStamp = await UserVersionAsync(target.Id),
        });
        Assert.Equal(HttpStatusCode.Forbidden, assignment.StatusCode);
    }

    [Fact]
    public async Task StewardedRoleFullyCoveredByOneDelegationScopeCanBeAssigned()
    {
        var delegateUser = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var target = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"rbac-covered-{Guid.NewGuid():N}", [PermissionA, PermissionB]);
        var delegation = await CreateDelegationAsync(owner, delegateUser.Id, DateTimeOffset.UtcNow.AddHours(1),
            [role.Id], [PermissionA, PermissionB]);

        using var delegated = await factory.CreateAuthenticatedClientAsync(delegateUser.Email, delegateUser.Password);
        var me = await delegated.GetFromJsonAsync<JsonElement>("/api/v1/identity/access/me");
        var scope = Assert.Single(me.GetProperty("administrationScope").GetProperty("delegationScopes").EnumerateArray());
        Assert.Contains(role.Id, scope.GetProperty("assignableRoleIds").EnumerateArray().Select(item => item.GetGuid()));

        var assignment = await delegated.PutAsJsonAsync($"/api/v1/identity/access/users/{target.Id}/roles", new
        {
            roleIds = new[] { role.Id },
            delegationId = delegation.Id,
            concurrencyStamp = await UserVersionAsync(target.Id),
        });
        Assert.Equal(HttpStatusCode.OK, assignment.StatusCode);
        await AssertUserHasRoleAsync(target.Id, role.Id);
    }

    [Fact]
    public async Task DelegationRejectsNonDelegablePermissionAndExpiredOrRevokedBoundary()
    {
        var delegateUser = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var nonDelegable = await owner.PostAsJsonAsync("/api/v1/identity/access/delegations", new
        {
            granteeUserId = delegateUser.Id,
            expiresAt = DateTimeOffset.UtcNow.AddHours(1),
            permissionKeys = new[] { Permission },
            stewardedRoleIds = Array.Empty<Guid>(),
            canCreateRoles = true,
        });
        Assert.Equal(HttpStatusCode.BadRequest, nonDelegable.StatusCode);

        var boundary = await CreateDelegationAsync(owner, delegateUser.Id, DateTimeOffset.UtcNow.AddHours(1));
        using var delegateClient = await factory.CreateAuthenticatedClientAsync(delegateUser.Email, delegateUser.Password);
        var delegateRoles = await delegateClient.GetAsync("/api/v1/identity/access/roles");
        Assert.True(delegateRoles.StatusCode == HttpStatusCode.OK,
            $"{delegateRoles.StatusCode}: {await delegateRoles.Content.ReadAsStringAsync()}");

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var delegation = await db.AuthorizationDelegations.SingleAsync(item => item.Id == boundary.Id);
            delegation.ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(-1);
            await db.SaveChangesAsync();
        }

        var expiredAccess = await delegateClient.GetAsync("/api/v1/identity/access/roles");
        Assert.Equal(HttpStatusCode.Forbidden, expiredAccess.StatusCode);
    }

    [Fact]
    public async Task DelegatedRoleStewardshipAndBoundaryAreEnforced()
    {
        var delegateUser = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var ordinaryTarget = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var stewarded = await CreateRoleAsync(owner, $"rbac-stewarded-{Guid.NewGuid():N}", []);
        var boundary = await CreateDelegationAsync(owner, delegateUser.Id, DateTimeOffset.UtcNow.AddHours(1), [stewarded.Id]);
        using var delegateClient = await factory.CreateAuthenticatedClientAsync(delegateUser.Email, delegateUser.Password);

        var edit = await delegateClient.PutAsJsonAsync($"/api/v1/identity/access/roles/{stewarded.Id}", new
        {
            name = stewarded.Name,
            displayName = stewarded.Name,
            description = "Delegate edited",
            permissionKeys = Array.Empty<string>(),
            concurrencyStamp = stewarded.Version,
        });
        Assert.Equal(HttpStatusCode.OK, edit.StatusCode);

        var assignSelf = await delegateClient.PutAsJsonAsync($"/api/v1/identity/access/users/{delegateUser.Id}/roles", new
        {
            roleIds = new[] { stewarded.Id },
            concurrencyStamp = await UserVersionAsync(delegateUser.Id),
        });
        Assert.Equal(HttpStatusCode.BadRequest, assignSelf.StatusCode);

        var assignOwner = await delegateClient.PutAsJsonAsync($"/api/v1/identity/access/users/{factory.OwnerId}/roles", new
        {
            roleIds = new[] { stewarded.Id },
            concurrencyStamp = await UserVersionAsync(factory.OwnerId),
        });
        Assert.Equal(HttpStatusCode.Forbidden, assignOwner.StatusCode);

        var assignOrdinary = await delegateClient.PutAsJsonAsync($"/api/v1/identity/access/users/{ordinaryTarget.Id}/roles", new
        {
            roleIds = new[] { stewarded.Id },
            delegationId = boundary.Id,
            concurrencyStamp = await UserVersionAsync(ordinaryTarget.Id),
        });
        Assert.Equal(HttpStatusCode.OK, assignOrdinary.StatusCode);
        Assert.NotEqual(Guid.Empty, boundary.Id);
    }

    [Fact]
    public async Task DelegatedRoleEditMustCoverCurrentAndReplacementPermissions()
    {
        var narrowDelegate = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var broadDelegate = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"rbac-edit-current-boundary-{Guid.NewGuid():N}", [PermissionA, PermissionB]);
        await CreateDelegationAsync(owner, narrowDelegate.Id, DateTimeOffset.UtcNow.AddHours(1),
            [role.Id], [PermissionA]);

        using var narrowClient = await factory.CreateAuthenticatedClientAsync(narrowDelegate.Email, narrowDelegate.Password);
        var narrowEdit = await narrowClient.PutAsJsonAsync($"/api/v1/identity/access/roles/{role.Id}", new
        {
            name = role.Name,
            displayName = role.Name,
            description = "Narrow delegate must not weaken an uncovered current permission",
            permissionKeys = new[] { PermissionA },
            concurrencyStamp = role.Version,
        });
        Assert.Equal(HttpStatusCode.Forbidden, narrowEdit.StatusCode);
        Assert.Equal(new[] { PermissionA, PermissionB }.Order(), (await RolePermissionsAsync(role.Id)).Order());

        var broadDelegation = await CreateDelegationAsync(owner, broadDelegate.Id, DateTimeOffset.UtcNow.AddHours(1),
            [role.Id], [PermissionA, PermissionB]);
        using var broadClient = await factory.CreateAuthenticatedClientAsync(broadDelegate.Email, broadDelegate.Password);
        var broadEdit = await broadClient.PutAsJsonAsync($"/api/v1/identity/access/roles/{role.Id}", new
        {
            name = role.Name,
            displayName = role.Name,
            description = "Broad delegate edit",
            permissionKeys = new[] { PermissionA },
            concurrencyStamp = role.Version,
        });
        Assert.Equal(HttpStatusCode.OK, broadEdit.StatusCode);
        Assert.Equal([PermissionA], await RolePermissionsAsync(role.Id));
        Assert.NotEqual(Guid.Empty, broadDelegation.Id);

        var ownerEdit = await owner.PutAsJsonAsync($"/api/v1/identity/access/roles/{role.Id}", new
        {
            name = role.Name,
            displayName = role.Name,
            description = "Owner edit",
            permissionKeys = Array.Empty<string>(),
            concurrencyStamp = await RoleVersionAsync(role.Id),
        });
        Assert.Equal(HttpStatusCode.OK, ownerEdit.StatusCode);
        Assert.Empty(await RolePermissionsAsync(role.Id));
    }

    [Fact]
    public async Task DelegatedDeleteRequiresSelectedScopeToCoverCurrentRolePermissions()
    {
        var delegateUser = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"rbac-delete-boundary-{Guid.NewGuid():N}", [PermissionA]);
        var delegation = await CreateDelegationAsync(owner, delegateUser.Id, DateTimeOffset.UtcNow.AddHours(1),
            [role.Id], [PermissionA]);

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var permissions = await db.AuthorizationDelegationPermissions
                .Where(item => item.DelegationId == delegation.Id).ToListAsync();
            db.AuthorizationDelegationPermissions.RemoveRange(permissions);
            await db.SaveChangesAsync();
        }

        using var delegated = await factory.CreateAuthenticatedClientAsync(delegateUser.Email, delegateUser.Password);
        using var deleteRequest = new HttpRequestMessage(
            HttpMethod.Delete, new Uri($"/api/v1/identity/access/roles/{role.Id}", UriKind.Relative))
        {
            Content = JsonContent.Create(new { concurrencyStamp = role.Version }),
        };
        Assert.Equal(HttpStatusCode.Forbidden, (await delegated.SendAsync(deleteRequest)).StatusCode);
    }

    [Fact]
    public async Task AuditContainsActorCorrelationMfaAndBeforeAfterAndAuditFailureRollsBackMutation()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"rbac-audit-{Guid.NewGuid():N}", []);
        var audit = await owner.GetFromJsonAsync<AuditResponse[]>("/api/v1/identity/access/audit");
        var created = Assert.Single(audit!, item => item.Action == "role.created" && item.TargetRoleId == role.Id);
        Assert.Equal(factory.OwnerId, created.ActorUserId);
        Assert.NotEmpty(created.CorrelationId);
        Assert.False(created.MfaAuthenticated);
        Assert.NotEmpty(created.BeforeJson);
        Assert.NotEmpty(created.AfterJson);

        var failedRoleName = $"rbac-audit-failure-{Guid.NewGuid():N}";
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var roleEntity = new IdentityRole<Guid>
            {
                Name = failedRoleName,
                NormalizedName = failedRoleName.ToUpperInvariant(),
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            };
            db.Roles.Add(roleEntity);
            db.RoleMetadata.Add(new RoleMetadata
            {
                RoleId = roleEntity.Id,
                DisplayName = failedRoleName,
                Description = "failure",
                IsSystem = false,
                IsBuiltIn = false,
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            });
            db.AuthorizationAuditEvents.Add(new AuthorizationAuditEvent
            {
                ActorUserId = factory.OwnerId,
                TargetRoleId = roleEntity.Id,
                Action = "test.audit.failure",
                Details = new string('x', 5001),
                BeforeJson = "{}",
                AfterJson = "{}",
                CorrelationId = "test",
                MfaAuthenticated = true,
                OccurredAt = DateTimeOffset.UtcNow,
            });
            await Assert.ThrowsAnyAsync<DbUpdateException>(() => db.SaveChangesAsync());
        }

        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.False(await verifyDb.Roles.AnyAsync(item => item.Name == failedRoleName));
    }

    private async Task<bool> AuthorizePermissionAsync(Guid userId, string permission = Permission, bool ownerClaim = false)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var authorization = scope.ServiceProvider.GetRequiredService<IAuthorizationService>();
        var claims = new List<Claim> { new(ClaimTypes.NameIdentifier, userId.ToString()) };
        if (ownerClaim) claims.Add(new Claim(ClaimTypes.Role, AuthRoles.Owner));
        var principal = new ClaimsPrincipal(new ClaimsIdentity(claims, "test"));
        var result = await authorization.AuthorizeAsync(principal, null, $"permission:{permission}");
        return result.Succeeded;
    }

    private async Task SetOverrideAsync(Guid groupId, Guid userId, AccessGroupMembershipOverride value, bool upstreamPresent)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var membership = await db.AccessGroupMemberships.SingleAsync(item => item.GroupId == groupId && item.UserId == userId);
        membership.Override = value;
        membership.IsUpstreamPresent = upstreamPresent;
        membership.UpdatedAt = DateTimeOffset.UtcNow;
        await db.SaveChangesAsync();
    }

    private async Task SetGroupActiveAsync(Guid groupId, bool active)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var group = await db.AccessGroups.SingleAsync(item => item.Id == groupId);
        group.IsActive = active;
        await db.SaveChangesAsync();
    }

    private async Task<RoleResponse> CreateRoleAsync(HttpClient owner, string name, IReadOnlyCollection<string> permissions)
    {
        var response = await owner.PostAsJsonAsync("/api/v1/identity/access/roles", new
        {
            name,
            displayName = name,
            description = "Integration role",
            permissionKeys = permissions,
        });
        await EnsureSuccess(response);
        return (await response.Content.ReadFromJsonAsync<RoleResponse>())!;
    }

    private async Task<HttpResponseMessage> AssignRoleAsync(HttpClient owner, Guid userId, Guid roleId)
    {
        return await owner.PutAsJsonAsync($"/api/v1/identity/access/users/{userId}/roles", new
        {
            roleIds = new[] { roleId },
            concurrencyStamp = await UserVersionAsync(userId),
        });
    }

    private async Task<DelegationResponse> CreateDelegationAsync(
        HttpClient owner, Guid userId, DateTimeOffset expiresAt, IReadOnlyCollection<Guid>? stewardedRoleIds = null,
        IReadOnlyCollection<string>? permissionKeys = null)
    {
        var response = await owner.PostAsJsonAsync("/api/v1/identity/access/delegations", new
        {
            granteeUserId = userId,
            expiresAt,
            permissionKeys = permissionKeys ?? Array.Empty<string>(),
            stewardedRoleIds = stewardedRoleIds ?? Array.Empty<Guid>(),
            canCreateRoles = true,
        });
        await EnsureSuccess(response);
        return (await response.Content.ReadFromJsonAsync<DelegationResponse>())!;
    }

    private async Task<RoleResponse> RoleInfoAsync(string name)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var role = await db.Roles.SingleAsync(item => item.Name == name);
        var metadata = await db.RoleMetadata.SingleAsync(item => item.RoleId == role.Id);
        return new RoleResponse(role.Id, role.Name!, metadata.ConcurrencyStamp, []);
    }

    private async Task<string> RoleVersionAsync(Guid roleId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        return await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().RoleMetadata
            .Where(item => item.RoleId == roleId).Select(item => item.ConcurrencyStamp).SingleAsync();
    }

    private async Task<string[]> RolePermissionsAsync(Guid roleId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        return await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().RolePermissions
            .Where(item => item.RoleId == roleId).Select(item => item.PermissionKey).OrderBy(item => item).ToArrayAsync();
    }

    private async Task<string> UserVersionAsync(Guid userId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        return (await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Users
            .Where(item => item.Id == userId).Select(item => item.ConcurrencyStamp).SingleAsync())!;
    }

    private async Task AssertUserStillHasSystemRoleAsync(Guid userId, string roleName)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.True(await db.UserRoles.Where(assignment => assignment.UserId == userId).Join(db.Roles, assignment => assignment.RoleId, role => role.Id,
            (_, role) => new { role.Name }).AnyAsync(item => item.Name == roleName));
    }

    private async Task AssertUserHasRoleAsync(Guid userId, Guid roleId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.True(await db.UserRoles.AnyAsync(item => item.UserId == userId && item.RoleId == roleId));
    }

    private async Task AssertUserLacksRoleAsync(Guid userId, Guid roleId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.False(await db.UserRoles.AnyAsync(item => item.UserId == userId && item.RoleId == roleId));
    }

    private static async Task EnsureSuccess(HttpResponseMessage response)
    {
        if (!response.IsSuccessStatusCode)
            throw new Xunit.Sdk.XunitException($"Expected success, got {(int)response.StatusCode}: {await response.Content.ReadAsStringAsync()}");
    }

    private sealed record RoleResponse(Guid Id, string Name, string Version, IReadOnlyCollection<string> Permissions);
    private sealed record AssignmentResponse(Guid UserId, IReadOnlyCollection<Guid> RoleIds, string Version);
    private sealed record DelegationResponse(Guid Id, string Version);
    private sealed record AuditResponse(Guid? ActorUserId, Guid? TargetUserId, Guid? TargetRoleId, string Action,
        string Details, string BeforeJson, string AfterJson, string CorrelationId, bool MfaAuthenticated,
        DateTimeOffset OccurredAt);
}