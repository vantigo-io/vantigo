using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentitySystemStatusIntegrationTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task SystemStatusRequiresOwnerAndRedactsSecretsWhileReturningCoherentCounts()
    {
        using var anonymous = factory.CreateCookieClient();
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await anonymous.GetAsync("/api/v1/identity/owner/system-status")).StatusCode);

        var user = await factory.CreateUserWithCredentialsAsync(Vantigo.Contracts.Identity.AuthRoles.User);
        using var standard = await factory.CreateAuthenticatedClientAsync(user.Email, user.Password);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await standard.GetAsync("/api/v1/identity/owner/system-status")).StatusCode);

        using var owner = await factory.CreateOwnerClientAsync();
        var response = await owner.GetAsync("/api/v1/identity/owner/system-status");
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var status = await response.Content.ReadFromJsonAsync<JsonElement>();
        var total = status.GetProperty("total").GetInt32();
        var active = status.GetProperty("active").GetInt32();
        var disabled = status.GetProperty("disabled").GetInt32();
        Assert.True(total >= 2);
        Assert.InRange(active, 0, total - disabled);
        Assert.True(status.GetProperty("pendingInvitations").GetInt32() >= 0);
        Assert.False(status.GetProperty("staticOidcEnabled").GetBoolean());
        Assert.False(status.GetProperty("staticScimEnabled").GetBoolean());

        var payload = status.GetRawText();
        Assert.DoesNotContain(IdentityApiFactory.BootstrapSecret, payload, StringComparison.Ordinal);
        Assert.DoesNotContain(IdentityApiFactory.StaticScimToken, payload, StringComparison.Ordinal);
    }
}

[Collection(StaticScimApiCollection.Name)]
public sealed class StaticScimSystemStatusIntegrationTests(StaticScimIdentityApiFactory factory) : IAsyncLifetime
{
    public async Task InitializeAsync() => await factory.ResetIdentityStateAsync();
    public Task DisposeAsync() => Task.CompletedTask;

    [Fact]
    public async Task CountsExcludeUnavailableAccountsAndOnlyCountPendingInvitations()
    {
        var available = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var locked = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var disabled = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var scimInactive = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var now = DateTimeOffset.UtcNow;

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var owner = await db.Users.SingleAsync(user => user.Email == IdentityApiFactory.OwnerEmail);
            db.Users.Single(user => user.Id == locked.Id).LockoutEnd = now.AddMinutes(10);
            db.Users.Single(user => user.Id == disabled.Id).IsDisabled = true;
            db.ScimUserMappings.Add(new ScimUserMapping
            {
                ScimConnectionId = ScimConnection.StaticId,
                UserId = scimInactive.Id,
                ResourceId = "status-scim-inactive",
                ExternalId = "status-scim-inactive",
                UserName = scimInactive.Email,
                UpstreamActive = false,
                LastSynchronizedAt = now,
                ETag = "status-scim-inactive-etag",
                CreatedAt = now,
                UpdatedAt = now,
            });

            db.Invitations.AddRange(
                Invitation(now.AddHours(-1), now.AddHours(1), owner.Id, "pending-status@example.test"),
                Invitation(now.AddHours(-1), now.AddHours(1), owner.Id, "revoked-status@example.test", revoked: true),
                Invitation(now.AddHours(-1), now.AddHours(1), owner.Id, "accepted-status@example.test", accepted: true),
                Invitation(now.AddDays(-2), now.AddHours(-1), owner.Id, "expired-status@example.test"));
            await db.SaveChangesAsync();
        }

        using var ownerClient = await factory.CreateOwnerClientAsync();
        var status = await ownerClient.GetFromJsonAsync<JsonElement>("/api/v1/identity/owner/system-status");

        Assert.Equal(5, status.GetProperty("total").GetInt32());
        Assert.Equal(2, status.GetProperty("active").GetInt32());
        Assert.Equal(1, status.GetProperty("disabled").GetInt32());
        Assert.Equal(1, status.GetProperty("pendingInvitations").GetInt32());
        var payload = status.GetRawText();
        Assert.DoesNotContain("pending-status@example.test", payload, StringComparison.Ordinal);
        Assert.DoesNotContain("revoked-status@example.test", payload, StringComparison.Ordinal);
        Assert.DoesNotContain("expired-status@example.test", payload, StringComparison.Ordinal);
        _ = available;
    }

    [Fact]
    public async Task AuthenticatedScimIngressIsReportedBestEffortWithoutExposingToken()
    {
        using var scim = factory.CreateClient();
        scim.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", IdentityApiFactory.StaticScimToken);
        Assert.Equal(HttpStatusCode.OK,
            (await scim.GetAsync(ScimProtocolService.BasePath + "/ServiceProviderConfig")).StatusCode);

        using var owner = await factory.CreateOwnerClientAsync();
        var status = await owner.GetFromJsonAsync<JsonElement>("/api/v1/identity/owner/system-status");
        Assert.True(status.GetProperty("staticScimEnabled").GetBoolean());
        Assert.False(status.GetProperty("staticOidcEnabled").GetBoolean());
        Assert.NotNull(status.GetProperty("lastAuthenticatedScimRequestAtUtc").GetString());
        Assert.DoesNotContain(IdentityApiFactory.StaticScimToken, status.GetRawText(), StringComparison.Ordinal);
    }

    private static Invitation Invitation(
        DateTimeOffset createdAt,
        DateTimeOffset expiresAt,
        Guid invitedByUserId,
        string email,
        bool revoked = false,
        bool accepted = false) => new()
        {
            Email = email,
            NormalizedEmail = email.ToUpperInvariant(),
            Role = AuthRoles.User,
            TokenHash = Guid.NewGuid().ToString("N"),
            CreatedAt = createdAt,
            ExpiresAt = expiresAt,
            RevokedAt = revoked ? createdAt.AddMinutes(1) : null,
            AcceptedAt = accepted ? createdAt.AddMinutes(2) : null,
            InvitedByUserId = invitedByUserId,
        };
}