using System.Net;
using System.Net.Http.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentityAccountEndpointsTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task OwnerManagement_IsUnauthorizedForAnonymousAndForbiddenForStandardUser()
    {
        using var anonymous = factory.CreateCookieClient();
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await anonymous.GetAsync("/api/v1/identity/owner/users")).StatusCode);

        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var standard = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        var response = await standard.GetAsync("/api/v1/identity/owner/users");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task OwnerMutation_RequiresCsrfToken()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        owner.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");

        var response = await owner.PostAsJsonAsync("/api/v1/identity/owner/users", new
        {
            displayName = "CSRF blocked",
            email = $"csrf-{Guid.NewGuid():N}@integration.test",
            role = AuthRoles.User,
            password = "IntegrationUserPassword123",
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        Assert.Equal("csrf_validation_failed", (await response.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);
    }

    [Fact]
    public async Task OwnerCannotMutateOwnAccount()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var response = await owner.PutAsJsonAsync($"/api/v1/identity/owner/users/{factory.OwnerId}", new
        {
            displayName = "Attempted self mutation",
            email = IdentityApiFactory.OwnerEmail,
            role = AuthRoles.Owner,
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        Assert.Equal("invalid_request", (await response.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);
    }

    [Fact]
    public async Task UserEndpoints_ReturnCoherentContractAndDeterministicOwnerOrdering()
    {
        using var owner = await factory.CreateOwnerClientAsync();
        var email = $"managed-{Guid.NewGuid():N}@integration.test";
        var create = await owner.PostAsJsonAsync("/api/v1/identity/owner/users", new
        {
            displayName = "Managed User",
            email,
            role = AuthRoles.User,
            password = "IntegrationUserPassword123",
        });

        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var created = await create.Content.ReadFromJsonAsync<ManagedUserResponse>();
        Assert.NotNull(created);
        Assert.Equal(email, created!.Email);
        Assert.Equal(AuthRoles.User, created.Role);
        Assert.True(created.Active);
        Assert.False(created.Disabled);
        Assert.False(created.LockedOut);
        Assert.Null(created.LockoutEnd);

        var users = await owner.GetFromJsonAsync<ManagedUserResponse[]>("/api/v1/identity/owner/users");
        Assert.NotNull(users);
        var ownerIndex = Array.FindIndex(users!, user => user.Role == AuthRoles.Owner);
        var userIndex = Array.FindIndex(users!, user => user.Email == email);
        Assert.True(ownerIndex >= 0 && userIndex >= 0 && ownerIndex < userIndex);

        var invalid = await owner.PostAsJsonAsync("/api/v1/identity/owner/users", new
        {
            displayName = "Invalid role",
            email = $"invalid-role-{Guid.NewGuid():N}@integration.test",
            role = "Administrator",
            password = "IntegrationUserPassword123",
        });
        Assert.Equal(HttpStatusCode.BadRequest, invalid.StatusCode);
        var invalidError = await invalid.Content.ReadFromJsonAsync<ErrorResponse>();
        Assert.Contains("role", invalidError!.Error.Fields!.Keys);
    }

    [Fact]
    public async Task Disable_PreservesTransientLockoutAndRejectsLoginAndStaleBusinessSession()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var targetClient = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        var lockoutEnd = DateTimeOffset.UtcNow.AddMinutes(10);
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var target = await db.Users.SingleAsync(user => user.Id == credentials.Id);
            target.LockoutEnd = lockoutEnd;
            await db.SaveChangesAsync();
        }

        using var owner = await factory.CreateOwnerClientAsync();
        var disable = await owner.PostAsync($"/api/v1/identity/owner/users/{credentials.Id}/disable", null);
        Assert.Equal(HttpStatusCode.OK, disable.StatusCode);
        var disabled = await disable.Content.ReadFromJsonAsync<ManagedUserResponse>();
        Assert.NotNull(disabled);
        Assert.True(disabled!.Disabled);
        Assert.True(disabled.LockedOut);
        Assert.NotNull(disabled.LockoutEnd);
        Assert.InRange((disabled.LockoutEnd!.Value - lockoutEnd).Duration(), TimeSpan.Zero, TimeSpan.FromSeconds(2));
        Assert.False(disabled.Active);

        Assert.Equal(HttpStatusCode.Unauthorized,
            (await targetClient.GetAsync("/api/v1/identity/session")).StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await targetClient.GetAsync("/api/v1/customers")).StatusCode);

        using var login = await factory.CreateAntiforgeryClientAsync();
        var rejected = await login.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = credentials.Email,
            password = credentials.Password,
        });
        Assert.Equal(HttpStatusCode.TooManyRequests, rejected.StatusCode);
        var error = await rejected.Content.ReadFromJsonAsync<ErrorResponse>();
        Assert.Equal("account_locked", error!.Error.Code);
        Assert.DoesNotContain("disabled", error.Error.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task ConcurrentOwnerDemotions_LeaveOneActiveOwnerAndReturnConflict()
    {
        var first = await factory.CreateUserWithCredentialsAsync(AuthRoles.Owner);
        var second = await factory.CreateUserWithCredentialsAsync(AuthRoles.Owner);
        using var owner = await factory.CreateOwnerClientAsync();

        var firstRequest = owner.PutAsJsonAsync($"/api/v1/identity/owner/users/{first.Id}", new
        {
            displayName = "Demoted first",
            email = first.Email,
            role = AuthRoles.User,
        });
        var secondRequest = owner.PutAsJsonAsync($"/api/v1/identity/owner/users/{second.Id}", new
        {
            displayName = "Demoted second",
            email = second.Email,
            role = AuthRoles.User,
        });
        var responses = await Task.WhenAll(firstRequest, secondRequest);

        Assert.Equal(1, responses.Count(response => response.StatusCode == HttpStatusCode.OK));
        Assert.Equal(1, responses.Count(response => response.StatusCode == HttpStatusCode.Conflict));
        var conflict = responses.Single(response => response.StatusCode == HttpStatusCode.Conflict);
        var conflictCode = (await conflict.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code;
        Assert.Contains(conflictCode, new[] { "last_active_owner", "account_conflict" });

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var ownerRoleId = await db.Roles
            .Where(role => role.Name == AuthRoles.Owner)
            .Select(role => role.Id)
            .SingleAsync();
        var activeOwners = await db.UserRoles
            .Join(db.Users, assignment => assignment.UserId, user => user.Id,
                (assignment, user) => new { assignment, user })
            .CountAsync(item => item.assignment.RoleId == ownerRoleId &&
                (item.user.Id == first.Id || item.user.Id == second.Id) &&
                !item.user.IsDisabled &&
                (!item.user.LockoutEnd.HasValue || item.user.LockoutEnd <= DateTimeOffset.UtcNow));
        Assert.Equal(1, activeOwners);
    }

    private sealed record ErrorResponse(Error Error);
    private sealed record Error(string Code, string Message, Dictionary<string, string[]>? Fields = null);
    private sealed record ManagedUserResponse(
        Guid Id,
        string DisplayName,
        string? Email,
        string Role,
        bool Active,
        bool Disabled,
        bool LockedOut,
        DateTimeOffset? LockoutEnd,
        bool TwoFactorEnabled);
}