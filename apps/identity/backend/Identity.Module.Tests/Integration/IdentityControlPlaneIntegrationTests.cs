using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Security.Claims;
using System.Text.Json;

using Microsoft.AspNetCore.Authorization;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentityControlPlaneIntegrationTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task ControlPlaneRequiresAuthenticationAndOwnerAuthorization()
    {
        using var anonymous = factory.CreateCookieClient();
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await anonymous.GetAsync("/api/v1/identity/access/federation-connections")).StatusCode);

        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var standard = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await standard.GetAsync("/api/v1/identity/access/groups")).StatusCode);
    }

    [Fact]
    public async Task OwnerCanManageFederationConnectionWithoutSecretDisclosureAndRoundTripsJit()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var create = await owner.PostAsJsonAsync("/api/v1/identity/access/federation-connections", new
        {
            providerType = "Entra",
            displayName = $"Entra {Guid.NewGuid():N}",
            authority = "HTTPS://Issuer.Example.TEST/tenant/",
            clientId = "client-id",
            clientSecretReference = "VANTIGO_SSO_ENTRA_CLIENT_SECRET",
            allowedDomains = new[] { "Example.TEST", "example.test." },
            isEnabled = true,
            isDefault = false,
            jitCreationMode = "CreateUser",
        });
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var createdJson = await create.Content.ReadAsStringAsync();
        Assert.DoesNotContain("secret-value-that-must-not-leak", createdJson, StringComparison.Ordinal);
        Assert.DoesNotContain("integration-entra-secret", createdJson, StringComparison.Ordinal);
        var created = await create.Content.ReadFromJsonAsync<FederationResponse>();
        Assert.NotNull(created);
        Assert.Equal("VANTIGO_SSO_ENTRA_CLIENT_SECRET", created!.ClientSecretReference);
        Assert.Equal("Draft", created.ValidationState);
        Assert.Equal(new[] { "example.test" }, created.AllowedDomains);
        Assert.Equal("CreateUser", created.JitCreationMode);
        Assert.Equal("https://issuer.example.test/tenant", created.Authority);

        var list = await owner.GetAsync("/api/v1/identity/access/federation-connections");
        Assert.Equal(HttpStatusCode.OK, list.StatusCode);
        Assert.DoesNotContain("secret-value-that-must-not-leak", await list.Content.ReadAsStringAsync(), StringComparison.Ordinal);
        Assert.DoesNotContain("integration-entra-secret", await list.Content.ReadAsStringAsync(), StringComparison.Ordinal);

        var get = await owner.GetAsync($"/api/v1/identity/access/federation-connections/{created.Id}");
        Assert.Equal(HttpStatusCode.OK, get.StatusCode);
        Assert.DoesNotContain("secret-value-that-must-not-leak", await get.Content.ReadAsStringAsync(), StringComparison.Ordinal);
        Assert.DoesNotContain("integration-entra-secret", await get.Content.ReadAsStringAsync(), StringComparison.Ordinal);

        var update = await owner.PutAsJsonAsync($"/api/v1/identity/access/federation-connections/{created.Id}", new
        {
            providerType = "Google",
            displayName = created.DisplayName,
            authority = created.Authority,
            clientId = created.ClientId,
            clientSecretReference = "VANTIGO_SSO_GOOGLE_CLIENT_SECRET",
            allowedDomains = new[] { "example.org" },
            isEnabled = false,
            isDefault = false,
            jitCreationMode = "Disabled",
            concurrencyStamp = created.ConcurrencyStamp,
        });
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        Assert.DoesNotContain("secret-value-that-must-not-leak", await update.Content.ReadAsStringAsync(), StringComparison.Ordinal);
        Assert.DoesNotContain("integration-google-secret", await update.Content.ReadAsStringAsync(), StringComparison.Ordinal);
        var updated = await update.Content.ReadFromJsonAsync<FederationResponse>();
        Assert.NotNull(updated);
        Assert.Equal("VANTIGO_SSO_GOOGLE_CLIENT_SECRET", updated!.ClientSecretReference);
        Assert.Equal("Draft", updated.ValidationState);
        Assert.Equal("Disabled", updated.JitCreationMode);

        var enable = await owner.PostAsJsonAsync($"/api/v1/identity/access/federation-connections/{created.Id}/enable", new { concurrencyStamp = updated.ConcurrencyStamp });
        Assert.Equal(HttpStatusCode.BadRequest, enable.StatusCode);
        Assert.Contains("validation_required", await enable.Content.ReadAsStringAsync(), StringComparison.Ordinal);
        Assert.Equal(HttpStatusCode.NoContent,
            (await owner.SendAsync(new HttpRequestMessage(HttpMethod.Delete, $"/api/v1/identity/access/federation-connections/{created.Id}")
            { Content = JsonContent.Create(new { concurrencyStamp = (await owner.GetFromJsonAsync<FederationResponse>($"/api/v1/identity/access/federation-connections/{created.Id}"))!.ConcurrencyStamp }) })).StatusCode);

        await using var auditScope = factory.Services.CreateAsyncScope();
        var auditDb = auditScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var connectionAudit = await auditDb.AuthorizationAuditEvents.AsNoTracking()
            .Where(item => item.Action == "federation_connection.created" && item.AfterJson.Contains(created.DisplayName))
            .OrderByDescending(item => item.Id)
            .FirstOrDefaultAsync();
        Assert.NotNull(connectionAudit);
        Assert.DoesNotContain("secret-value-that-must-not-leak", connectionAudit!.BeforeJson + connectionAudit.AfterJson, StringComparison.Ordinal);
        Assert.DoesNotContain("integration-entra-secret", connectionAudit.BeforeJson + connectionAudit.AfterJson, StringComparison.Ordinal);
        Assert.Contains("ClientSecretReference", connectionAudit.AfterJson, StringComparison.Ordinal);
        Assert.DoesNotContain("secret-value-that-must-not-leak", connectionAudit.AfterJson, StringComparison.Ordinal);
    }

    [Fact]
    public async Task OwnerCanValidateThenEnableAndMakeConnectionDefault()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var create = await owner.PostAsJsonAsync("/api/v1/identity/access/federation-connections", new
        {
            providerType = "Generic",
            displayName = $"Validated {Guid.NewGuid():N}",
            authority = "https://issuer.example.test",
            clientId = "client-id",
            clientSecretReference = "VANTIGO_SSO_INTEGRATION_CLIENT_SECRET",
            allowedDomains = new[] { "example.test" },
            isEnabled = true,
            isDefault = false,
            jitCreationMode = "Disabled",
        });
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var draft = (await create.Content.ReadFromJsonAsync<FederationResponse>())!;

        var validate = await owner.PostAsJsonAsync($"/api/v1/identity/access/federation-connections/{draft.Id}/validate", new { concurrencyStamp = draft.ConcurrencyStamp });
        Assert.Equal(HttpStatusCode.OK, validate.StatusCode);
        Assert.DoesNotContain("integration-federation-secret", await validate.Content.ReadAsStringAsync(), StringComparison.Ordinal);
        var validated = (await validate.Content.ReadFromJsonAsync<ValidationResponse>())!;
        Assert.True(validated.Succeeded);
        Assert.Equal("Succeeded", validated.Connection.ValidationState);

        var enable = await owner.PostAsJsonAsync($"/api/v1/identity/access/federation-connections/{draft.Id}/enable", new { concurrencyStamp = validated.Connection.ConcurrencyStamp, isDefault = true });
        Assert.Equal(HttpStatusCode.OK, enable.StatusCode);
        Assert.DoesNotContain("integration-federation-secret", await enable.Content.ReadAsStringAsync(), StringComparison.Ordinal);
        var enabled = (await enable.Content.ReadFromJsonAsync<FederationResponse>())!;
        Assert.True(enabled.IsEnabled);
        Assert.True(enabled.IsDefault);
        Assert.Equal("https://issuer.example.test/authorize", enabled.ValidatedAuthorizationEndpoint);
    }

    [Fact]
    public async Task FederationDeleteWithFederatedIdentityReturnsConflictAndPreservesIdentity()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var create = await owner.PostAsJsonAsync("/api/v1/identity/access/federation-connections", new
        {
            providerType = "Generic",
            displayName = $"Protected {Guid.NewGuid():N}",
            authority = "https://issuer.protected.example.test",
            clientId = "protected-client",
            clientSecretReference = "VANTIGO_SSO_INTEGRATION_CLIENT_SECRET",
            allowedDomains = Array.Empty<string>(),
            jitCreationMode = "Disabled",
        });
        var connection = (await create.Content.ReadFromJsonAsync<FederationResponse>())!;
        var userId = await factory.CreateUserAsync(AuthRoles.User);
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            db.FederatedIdentities.Add(new FederatedIdentity
            {
                ConnectionId = connection.Id,
                Issuer = connection.Authority,
                Subject = $"protected-{Guid.NewGuid():N}",
                UserId = userId,
                CreatedAt = DateTimeOffset.UtcNow,
                UpdatedAt = DateTimeOffset.UtcNow,
            });
            await db.SaveChangesAsync();
        }

        var response = await owner.SendAsync(new HttpRequestMessage(
            HttpMethod.Delete, $"/api/v1/identity/access/federation-connections/{connection.Id}")
        {
            Content = JsonContent.Create(new { concurrencyStamp = connection.ConcurrencyStamp }),
        });
        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.True(await verifyDb.FederationConnections.AnyAsync(item => item.Id == connection.Id));
        Assert.True(await verifyDb.FederatedIdentities.AnyAsync(item => item.ConnectionId == connection.Id));
    }

    [Fact]
    public async Task ReferenceRotationInvalidatesValidationAndDisablesConnection()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var create = await owner.PostAsJsonAsync("/api/v1/identity/access/federation-connections", new
        {
            providerType = "Generic",
            displayName = $"Rotation {Guid.NewGuid():N}",
            authority = "https://issuer.example.test",
            clientId = "client-id",
            clientSecretReference = "VANTIGO_SSO_INTEGRATION_CLIENT_SECRET",
            allowedDomains = new[] { "example.test" },
            jitCreationMode = "Disabled",
        });
        var draft = (await create.Content.ReadFromJsonAsync<FederationResponse>())!;
        var validate = await owner.PostAsJsonAsync($"/api/v1/identity/access/federation-connections/{draft.Id}/validate", new { concurrencyStamp = draft.ConcurrencyStamp });
        var validated = (await validate.Content.ReadFromJsonAsync<ValidationResponse>())!.Connection;

        var update = await owner.PutAsJsonAsync($"/api/v1/identity/access/federation-connections/{draft.Id}", new
        {
            providerType = "Generic",
            displayName = draft.DisplayName,
            authority = draft.Authority,
            clientId = draft.ClientId,
            clientSecretReference = "VANTIGO_SSO_ROTATED_CLIENT_SECRET",
            allowedDomains = new[] { "example.test" },
            jitCreationMode = "Disabled",
            concurrencyStamp = validated.ConcurrencyStamp,
        });
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        var rotated = (await update.Content.ReadFromJsonAsync<FederationResponse>())!;
        Assert.False(rotated.IsEnabled);
        Assert.Equal("Draft", rotated.ValidationState);
        Assert.Null(rotated.ValidatedAuthorizationEndpoint);
    }

    [Fact]
    public async Task FederationAuthorityAndDomainsAreValidated()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var request = new
        {
            providerType = "Generic",
            displayName = "invalid federation",
            authority = "http://unsafe.example.test",
            clientId = "client",
            allowedDomains = new[] { "example.com" },
            jitCreationMode = "Disabled",
        };
        Assert.Equal(HttpStatusCode.BadRequest,
            (await owner.PostAsJsonAsync("/api/v1/identity/access/federation-connections", request)).StatusCode);

        var invalidDomain = new
        {
            providerType = "Generic",
            displayName = "invalid domain federation",
            authority = "https://issuer.example.test",
            clientId = "client",
            allowedDomains = new[] { "https://example.com" },
            jitCreationMode = "Disabled",
        };
        Assert.Equal(HttpStatusCode.BadRequest,
            (await owner.PostAsJsonAsync("/api/v1/identity/access/federation-connections", invalidDomain)).StatusCode);

        var invalidEnum = new
        {
            providerType = "999",
            displayName = "invalid enum federation",
            authority = "https://issuer.example.test",
            clientId = "client",
            allowedDomains = new[] { "example.com" },
            jitCreationMode = "999",
        };
        Assert.Equal(HttpStatusCode.BadRequest,
            (await owner.PostAsJsonAsync("/api/v1/identity/access/federation-connections", invalidEnum)).StatusCode);

        var invalidJit = new
        {
            providerType = "Generic",
            displayName = "invalid jit federation",
            authority = "https://issuer.example.test",
            clientId = "client",
            allowedDomains = new[] { "example.com" },
            jitCreationMode = "999",
        };
        Assert.Equal(HttpStatusCode.BadRequest,
            (await owner.PostAsJsonAsync("/api/v1/identity/access/federation-connections", invalidJit)).StatusCode);

        var disabledDefault = new
        {
            providerType = "Generic",
            displayName = "disabled default federation",
            authority = "https://issuer.example.test",
            clientId = "client",
            allowedDomains = new[] { "example.com" },
            isEnabled = false,
            isDefault = true,
            jitCreationMode = "Disabled",
        };
        Assert.Equal(HttpStatusCode.BadRequest,
            (await owner.PostAsJsonAsync("/api/v1/identity/access/federation-connections", disabledDefault)).StatusCode);
    }

    [Fact]
    public async Task OwnerCanManageGroupMembersAndOnlyUnprotectedRoles()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var groupCreate = await owner.PostAsJsonAsync("/api/v1/identity/access/groups", new
        {
            displayName = $"Local group {Guid.NewGuid():N}",
            isActive = true,
        });
        Assert.Equal(HttpStatusCode.Created, groupCreate.StatusCode);
        var group = await groupCreate.Content.ReadFromJsonAsync<GroupResponse>();
        Assert.NotNull(group);

        var member = await owner.PostAsJsonAsync($"/api/v1/identity/access/groups/{group!.Id}/members/{credentials.Id}", new { concurrencyStamp = group.ConcurrencyStamp });
        Assert.Equal(HttpStatusCode.OK, member.StatusCode);
        group = await member.Content.ReadFromJsonAsync<GroupResponse>();
        Assert.NotNull(group);
        var role = await CreateRoleAsync(owner, $"group-role-{Guid.NewGuid():N}");
        var mapping = await owner.PostAsJsonAsync($"/api/v1/identity/access/groups/{group!.Id}/role-mappings/{role.Id}", new { concurrencyStamp = group.ConcurrencyStamp });
        Assert.Equal(HttpStatusCode.OK, mapping.StatusCode);
        group = await mapping.Content.ReadFromJsonAsync<GroupResponse>();
        Assert.NotNull(group);

        var protectedRoleUpdate = await owner.PutAsJsonAsync($"/api/v1/identity/access/roles/{role.Id}", new
        {
            name = role.Name,
            displayName = role.Name,
            description = "Must remain ordinary",
            permissionKeys = new[] { "identity:manage" },
            concurrencyStamp = await RoleVersionAsync(role.Id),
        });
        Assert.Equal(HttpStatusCode.Forbidden, protectedRoleUpdate.StatusCode);

        var mappedRoleDelete = new HttpRequestMessage(HttpMethod.Delete, $"/api/v1/identity/access/roles/{role.Id}")
        {
            Content = JsonContent.Create(new { concurrencyStamp = await RoleVersionAsync(role.Id) }),
        };
        var mappedRoleDeleteResponse = await owner.SendAsync(mappedRoleDelete);
        Assert.Equal(HttpStatusCode.Forbidden, mappedRoleDeleteResponse.StatusCode);

        var ownerRole = await RoleIdAsync(AuthRoles.Owner);
        var protectedMapping = await owner.PostAsJsonAsync($"/api/v1/identity/access/groups/{group!.Id}/role-mappings/{ownerRole}", new { concurrencyStamp = group.ConcurrencyStamp });
        Assert.Equal(HttpStatusCode.BadRequest, protectedMapping.StatusCode);

        var currentStamp = group.ConcurrencyStamp;
        var removeMember = await owner.SendAsync(new HttpRequestMessage(HttpMethod.Delete, $"/api/v1/identity/access/groups/{group.Id}/members/{credentials.Id}")
        { Content = JsonContent.Create(new { concurrencyStamp = currentStamp }) });
        Assert.Equal(HttpStatusCode.OK, removeMember.StatusCode);
        var removeMemberGroup = await removeMember.Content.ReadFromJsonAsync<GroupResponse>();
        Assert.NotNull(removeMemberGroup);
        var removeMapping = await owner.SendAsync(new HttpRequestMessage(HttpMethod.Delete, $"/api/v1/identity/access/groups/{group.Id}/role-mappings/{role.Id}")
        { Content = JsonContent.Create(new { concurrencyStamp = removeMemberGroup!.ConcurrencyStamp }) });
        Assert.Equal(HttpStatusCode.OK, removeMapping.StatusCode);
        group = await removeMapping.Content.ReadFromJsonAsync<GroupResponse>();
        Assert.Equal(HttpStatusCode.NoContent,
            (await owner.SendAsync(new HttpRequestMessage(HttpMethod.Delete, $"/api/v1/identity/access/groups/{group!.Id}")
            { Content = JsonContent.Create(new { concurrencyStamp = group.ConcurrencyStamp }) })).StatusCode);

        await using var auditScope = factory.Services.CreateAsyncScope();
        var auditDb = auditScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var groupAudit = await auditDb.AuthorizationAuditEvents.AsNoTracking()
            .Where(item => item.Action.StartsWith("access_group.") && item.AfterJson.Contains(group.DisplayName))
            .ToListAsync();
        Assert.Contains(groupAudit, item => item.Action == "access_group.created");
        Assert.Contains(groupAudit, item => item.Action == "access_group.member_added");
        Assert.Contains(groupAudit, item => item.Action == "access_group.member_removed");
        Assert.Contains(groupAudit, item => item.Action == "access_group.role_mapped");
        Assert.Contains(groupAudit, item => item.Action == "access_group.role_unmapped");
        Assert.All(groupAudit, item =>
        {
            Assert.Equal(factory.OwnerId, item.ActorUserId);
            Assert.NotEmpty(item.CorrelationId);
            Assert.DoesNotContain("secret", item.BeforeJson + item.AfterJson, StringComparison.OrdinalIgnoreCase);
        });
    }

    [Fact]
    public async Task RoleEditRechecksGroupMappingAfterTheRoleLockIsReleased()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateRoleAsync(owner, $"group-lock-role-{Guid.NewGuid():N}");
        var groupCreate = await owner.PostAsJsonAsync("/api/v1/identity/access/groups", new
        {
            displayName = $"Group lock {Guid.NewGuid():N}",
            isActive = true,
        });
        var group = await groupCreate.Content.ReadFromJsonAsync<GroupResponse>();
        Assert.NotNull(group);

        await using var firstScope = factory.Services.CreateAsyncScope();
        var firstDb = firstScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var firstMutations = firstScope.ServiceProvider.GetRequiredService<AuthorizationMutationService>();
        await using var firstTransaction = await firstDb.Database.BeginTransactionAsync();
        await firstMutations.AcquireRoleMutationLockAsync(role.Id, CancellationToken.None);
        firstDb.AccessGroupRoleMappings.Add(new AccessGroupRoleMapping
        {
            GroupId = group!.Id,
            RoleId = role.Id,
            Source = AccessGroupSource.Local,
            CreatedAt = DateTimeOffset.UtcNow,
        });
        await firstDb.SaveChangesAsync();

        await using var secondScope = factory.Services.CreateAsyncScope();
        var secondDb = secondScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var secondMutations = secondScope.ServiceProvider.GetRequiredService<AuthorizationMutationService>();
        await using var secondTransaction = await secondDb.Database.BeginTransactionAsync();
        var protectedEdit = secondMutations.AuthorizeRoleEditAsync(
            factory.OwnerId, role.Id, role.Name, ["identity:manage"], CancellationToken.None);
        var completedBeforeRelease = await Task.WhenAny(protectedEdit, Task.Delay(TimeSpan.FromSeconds(2)));
        Assert.NotSame(protectedEdit, completedBeforeRelease);

        await firstTransaction.CommitAsync();
        var decision = await protectedEdit;
        Assert.NotNull(decision);
        await secondTransaction.RollbackAsync();
    }

    [Fact]
    public async Task OwnerMapsScimGroupAndDerivedAccessTracksMembershipAndGroupActivity()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var member = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var replacement = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var role = await CreateRoleAsync(owner, $"scim-group-role-{Guid.NewGuid():N}", ["customers:view"]);
        var directRole = await CreateRoleAsync(owner, $"direct-role-{Guid.NewGuid():N}", ["customers:create"]);
        var now = DateTimeOffset.UtcNow;
        var federation = new FederationConnection
        {
            ProviderKind = FederationProviderKind.Generic,
            DisplayName = $"SCIM group federation {Guid.NewGuid():N}",
            Authority = $"https://scim-group-{Guid.NewGuid():N}.integration.test",
            ClientId = "scim-group-client",
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        var scim = new ScimConnection
        {
            FederationConnectionId = federation.Id,
            IsEnabled = true,
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        var group = new AccessGroup
        {
            ScimConnectionId = scim.Id,
            DisplayName = $"SCIM mapped group {Guid.NewGuid():N}",
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
            db.ScimConnections.Add(scim);
            db.AccessGroups.Add(group);
            db.AccessGroupMemberships.Add(new AccessGroupMembership
            {
                GroupId = group.Id,
                UserId = member.Id,
                Source = AccessGroupSource.Scim,
                IsUpstreamPresent = true,
                UpdatedAt = now,
            });
            await db.SaveChangesAsync();
        }

        Assert.Equal(HttpStatusCode.OK, (await owner.PutAsJsonAsync($"/api/v1/identity/access/users/{member.Id}/roles", new
        {
            roleIds = new[] { directRole.Id },
            concurrencyStamp = await UserVersionAsync(member.Id),
        })).StatusCode);
        var mapping = await owner.PostAsJsonAsync($"/api/v1/identity/access/groups/{group.Id}/role-mappings/{role.Id}", new
        {
            concurrencyStamp = group.ConcurrencyStamp,
            scimConnectionId = scim.Id,
        });
        Assert.Equal(HttpStatusCode.OK, mapping.StatusCode);
        var mappedGroup = await mapping.Content.ReadFromJsonAsync<GroupResponse>();
        Assert.Equal(scim.Id, mappedGroup!.ScimConnectionId);
        Assert.Contains(role.Id, mappedGroup.RoleIds);
        Assert.True(await AuthorizePermissionAsync(member.Id, "customers:view"));
        Assert.True(await AuthorizePermissionAsync(member.Id, "customers:create"));
        Assert.False(await AuthorizePermissionAsync(member.Id, "customers:update"));

        var protectedRole = await CreateRoleAsync(owner, $"protected-group-role-{Guid.NewGuid():N}", ["identity:manage"]);
        var protectedResponse = await owner.PostAsJsonAsync($"/api/v1/identity/access/groups/{group.Id}/role-mappings/{protectedRole.Id}", new
        {
            concurrencyStamp = mappedGroup.ConcurrencyStamp,
            scimConnectionId = scim.Id,
        });
        Assert.Equal(HttpStatusCode.BadRequest, protectedResponse.StatusCode);

        var otherScim = new ScimConnection
        {
            FederationConnectionId = federation.Id,
            IsEnabled = true,
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            CreatedAt = now,
            UpdatedAt = now,
        };
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            // Keep the unique federation binding valid by using another federation.
            var otherFederation = new FederationConnection
            {
                ProviderKind = FederationProviderKind.Generic,
                DisplayName = $"Other SCIM federation {Guid.NewGuid():N}",
                Authority = $"https://other-scim-{Guid.NewGuid():N}.integration.test",
                ClientId = "other-scim-client",
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
                CreatedAt = now,
                UpdatedAt = now,
            };
            otherScim.FederationConnectionId = otherFederation.Id;
            db.FederationConnections.Add(otherFederation);
            db.ScimConnections.Add(otherScim);
            await db.SaveChangesAsync();
        }
        var wrongScope = await owner.PostAsJsonAsync($"/api/v1/identity/access/groups/{group.Id}/role-mappings/{role.Id}", new
        {
            concurrencyStamp = mappedGroup.ConcurrencyStamp,
            scimConnectionId = otherScim.Id,
        });
        Assert.Equal(HttpStatusCode.BadRequest, wrongScope.StatusCode);

        var bearer = await CreateScimBearerAsync();
        using var bearerClient = factory.CreateClient();
        bearerClient.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", bearer);
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await bearerClient.PostAsJsonAsync($"/api/v1/identity/access/groups/{group.Id}/role-mappings/{role.Id}", new { concurrencyStamp = mappedGroup.ConcurrencyStamp, scimConnectionId = scim.Id })).StatusCode);

        await SetMembershipAsync(group.Id, member.Id, false);
        Assert.False(await AuthorizePermissionAsync(member.Id, "customers:view"));
        Assert.True(await AuthorizePermissionAsync(member.Id, "customers:create"));
        await SetMembershipAsync(group.Id, member.Id, true);
        await SetGroupActiveAsync(group.Id, false);
        Assert.False(await AuthorizePermissionAsync(member.Id, "customers:view"));
        await SetGroupActiveAsync(group.Id, true);
        await SetMembershipAsync(group.Id, member.Id, false);
        await SetMembershipAsync(group.Id, replacement.Id, true);
        Assert.False(await AuthorizePermissionAsync(member.Id, "customers:view"));
        Assert.True(await AuthorizePermissionAsync(replacement.Id, "customers:view"));
    }

    [Fact]
    public async Task OwnerCannotMutateScimGroupContentButCanMapSafeRole()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var member = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var now = DateTimeOffset.UtcNow;
        var federation = new FederationConnection
        {
            ProviderKind = FederationProviderKind.Generic,
            DisplayName = $"SCIM protected group {Guid.NewGuid():N}",
            Authority = $"https://scim-protected-{Guid.NewGuid():N}.integration.test",
            ClientId = "scim-protected-client",
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
            DisplayName = $"SCIM protected group {Guid.NewGuid():N}",
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
                UserId = member.Id,
                Source = AccessGroupSource.Scim,
                IsUpstreamPresent = true,
                UpdatedAt = now,
            });
            await db.SaveChangesAsync();
        }

        static async Task AssertBlockedAsync(HttpResponseMessage response)
        {
            Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
            using var document = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
            Assert.Equal("scim_group_managed", document.RootElement.GetProperty("code").GetString());
        }

        await AssertBlockedAsync(await owner.PutAsJsonAsync($"/api/v1/identity/access/groups/{group.Id}", new
        {
            displayName = "Owner must not rename SCIM group",
            isActive = false,
            concurrencyStamp = group.ConcurrencyStamp,
        }));
        await AssertBlockedAsync(await owner.SendAsync(new HttpRequestMessage(
            HttpMethod.Delete, $"/api/v1/identity/access/groups/{group.Id}")
        { Content = JsonContent.Create(new { concurrencyStamp = group.ConcurrencyStamp }) }));
        await AssertBlockedAsync(await owner.PostAsJsonAsync($"/api/v1/identity/access/groups/{group.Id}/members/{member.Id}", new
        {
            concurrencyStamp = group.ConcurrencyStamp,
        }));
        await AssertBlockedAsync(await owner.SendAsync(new HttpRequestMessage(
            HttpMethod.Delete, $"/api/v1/identity/access/groups/{group.Id}/members/{member.Id}")
        { Content = JsonContent.Create(new { concurrencyStamp = group.ConcurrencyStamp }) }));

        var role = await CreateRoleAsync(owner, $"scim-safe-map-{Guid.NewGuid():N}", ["customers:view"]);
        var mapped = await owner.PostAsJsonAsync($"/api/v1/identity/access/groups/{group.Id}/role-mappings/{role.Id}", new
        {
            concurrencyStamp = group.ConcurrencyStamp,
            scimConnectionId = connection.Id,
        });
        Assert.Equal(HttpStatusCode.OK, mapped.StatusCode);

        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var savedGroup = await verifyDb.AccessGroups.SingleAsync(item => item.Id == group.Id);
        var savedMembership = await verifyDb.AccessGroupMemberships.SingleAsync(item => item.GroupId == group.Id && item.UserId == member.Id);
        Assert.Equal(group.DisplayName, savedGroup.DisplayName);
        Assert.True(savedGroup.IsActive);
        Assert.True(savedMembership.IsUpstreamPresent);
        Assert.True(await verifyDb.AccessGroupRoleMappings.AnyAsync(item => item.GroupId == group.Id && item.RoleId == role.Id));
        var rejectedAudits = await verifyDb.AuthorizationAuditEvents.AsNoTracking()
            .Where(item => item.Action.Contains("rejected"))
            .ToListAsync();
        Assert.Contains(rejectedAudits, item => item.Action == "access_group.update_rejected");
        Assert.Contains(rejectedAudits, item => item.Action == "access_group.delete_rejected");
        Assert.Contains(rejectedAudits, item => item.Action == "access_group.member_add_rejected");
        Assert.Contains(rejectedAudits, item => item.Action == "access_group.member_remove_rejected");
    }

    private async Task<Guid> RoleIdAsync(string name)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        return await db.Roles.Where(role => role.Name == name).Select(role => role.Id).SingleAsync();
    }

    private async Task<string> RoleVersionAsync(Guid roleId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        return await db.RoleMetadata.Where(item => item.RoleId == roleId).Select(item => item.ConcurrencyStamp).SingleAsync();
    }

    private async Task<string> UserVersionAsync(Guid userId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        return (await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Users
            .Where(item => item.Id == userId).Select(item => item.ConcurrencyStamp).SingleAsync())!;
    }

    private async Task<bool> AuthorizePermissionAsync(Guid userId, string permission)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var authorization = scope.ServiceProvider.GetRequiredService<IAuthorizationService>();
        var principal = new ClaimsPrincipal(new ClaimsIdentity(
            [new Claim(ClaimTypes.NameIdentifier, userId.ToString())], "test"));
        return (await authorization.AuthorizeAsync(principal, null, $"permission:{permission}")).Succeeded;
    }

    private async Task SetMembershipAsync(Guid groupId, Guid userId, bool present)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var membership = await db.AccessGroupMemberships.SingleOrDefaultAsync(item => item.GroupId == groupId && item.UserId == userId);
        if (membership is null)
        {
            membership = new AccessGroupMembership
            {
                GroupId = groupId,
                UserId = userId,
                Source = AccessGroupSource.Scim,
            };
            db.AccessGroupMemberships.Add(membership);
        }
        membership.Source = AccessGroupSource.Scim;
        membership.IsUpstreamPresent = present;
        membership.UpdatedAt = DateTimeOffset.UtcNow;
        await db.SaveChangesAsync();
    }

    private async Task SetGroupActiveAsync(Guid groupId, bool active)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var group = await db.AccessGroups.SingleAsync(item => item.Id == groupId);
        group.IsActive = active;
        group.UpdatedAt = DateTimeOffset.UtcNow;
        group.ConcurrencyStamp = Guid.NewGuid().ToString("N");
        await db.SaveChangesAsync();
    }

    private async Task<string> CreateScimBearerAsync()
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var now = DateTimeOffset.UtcNow;
        var federation = new FederationConnection
        {
            ProviderKind = FederationProviderKind.Generic,
            DisplayName = $"Bearer federation {Guid.NewGuid():N}",
            Authority = $"https://bearer-{Guid.NewGuid():N}.integration.test",
            ClientId = "bearer-client",
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
        db.FederationConnections.Add(federation);
        db.ScimConnections.Add(connection);
        await db.SaveChangesAsync();
        return (await scope.ServiceProvider.GetRequiredService<ScimTokenService>()
            .CreateAsync(connection.Id, connection.TokenVersion, now, CancellationToken.None)).Plaintext;
    }

    private static async Task<(Guid Id, string Name)> CreateRoleAsync(HttpClient owner, string name, IReadOnlyCollection<string>? permissionKeys = null)
    {
        var response = await owner.PostAsJsonAsync("/api/v1/identity/access/roles", new
        {
            name,
            displayName = name,
            description = "Group mapping test role",
            permissionKeys = permissionKeys ?? Array.Empty<string>(),
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var role = await response.Content.ReadFromJsonAsync<RoleResponse>();
        Assert.NotNull(role);
        return (role!.Id, role.Name);
    }

    private sealed record FederationResponse(
        Guid Id,
        string ProviderType,
        string DisplayName,
        string Authority,
        string ClientId,
        string[] AllowedDomains,
        bool IsEnabled,
        bool IsDefault,
        string JitCreationMode,
        int ConfigurationVersion,
        string ConcurrencyStamp,
        string? ClientSecretReference,
        string ValidationState,
        string? ValidationErrorCode,
        DateTimeOffset? ValidationCompletedAt,
        int? ValidatedConfigurationVersion,
        string? ValidatedIssuer,
        string? ValidatedDiscoveryEndpoint,
        string? ValidatedAuthorizationEndpoint,
        string? ValidatedTokenEndpoint,
        string? ValidatedJwksUri);

    private sealed record ValidationResponse(
        FederationResponse Connection,
        bool Succeeded,
        string Code,
        string Message);

    private sealed record GroupResponse(
        Guid Id,
        string DisplayName,
        string Source,
        Guid? ScimConnectionId,
        bool IsActive,
        DateTimeOffset CreatedAt,
        DateTimeOffset UpdatedAt,
        string ConcurrencyStamp,
        Guid[] MemberUserIds,
        Guid[] RoleIds);

    private sealed record RoleResponse(Guid Id, string Name);
}