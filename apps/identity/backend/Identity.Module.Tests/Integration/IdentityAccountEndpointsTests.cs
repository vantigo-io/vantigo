using System.Net;
using System.Net.Http.Headers;
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

        // Both demotions are individually legal (the acting owner remains), so
        // when the requests happen not to overlap both succeed; when they do
        // overlap, serializable isolation aborts one and it must surface as a
        // clean conflict, never a 500. Either way the owner set stays intact.
        var succeeded = responses.Count(response => response.StatusCode == HttpStatusCode.OK);
        Assert.InRange(succeeded, 1, 2);
        foreach (var conflict in responses.Where(response => response.StatusCode != HttpStatusCode.OK))
        {
            Assert.Equal(HttpStatusCode.Conflict, conflict.StatusCode);
            var conflictCode = (await conflict.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code;
            Assert.Contains(conflictCode, new[] { "last_active_owner", "account_conflict" });
        }

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var ownerRoleId = await db.Roles
            .Where(role => role.Name == AuthRoles.Owner)
            .Select(role => role.Id)
            .SingleAsync();
        var demotedTargets = await db.UserRoles
            .Join(db.Users, assignment => assignment.UserId, user => user.Id,
                (assignment, user) => new { assignment, user })
            .CountAsync(item => item.assignment.RoleId == ownerRoleId &&
                (item.user.Id == first.Id || item.user.Id == second.Id) &&
                !item.user.IsDisabled &&
                (!item.user.LockoutEnd.HasValue || item.user.LockoutEnd <= DateTimeOffset.UtcNow));
        Assert.Equal(2 - succeeded, demotedTargets);

        var activeOwners = await db.UserRoles
            .Join(db.Users, assignment => assignment.UserId, user => user.Id,
                (assignment, user) => new { assignment, user })
            .CountAsync(item => item.assignment.RoleId == ownerRoleId &&
                !item.user.IsDisabled &&
                (!item.user.LockoutEnd.HasValue || item.user.LockoutEnd <= DateTimeOffset.UtcNow));
        Assert.True(activeOwners >= 1);
    }

    [Fact]
    public async Task AccountProfile_IsolatedAndRejectsInvalidLanguageWithoutMutation()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var other = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        var before = await client.GetFromJsonAsync<AccountResponse>("/api/v1/identity/account");
        var invalid = await client.PutAsJsonAsync("/api/v1/identity/account/profile", new
        {
            displayName = "Changed",
            preferredLanguage = "fr",
        });
        Assert.Equal(HttpStatusCode.BadRequest, invalid.StatusCode);
        Assert.Equal("invalid_request", (await invalid.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);
        var after = await client.GetFromJsonAsync<AccountResponse>("/api/v1/identity/account");
        Assert.Equal(before!.DisplayName, after!.DisplayName);

        var foreign = await client.GetAsync($"/api/v1/identity/account/{other.Id}");
        Assert.Equal(HttpStatusCode.NotFound, foreign.StatusCode);
    }

    [Fact]
    public async Task AccountProfile_NorwegianLanguageRoundTripsCaseInsensitivelyAndNormalizesAutomatic()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        var blank = await client.PutAsJsonAsync("/api/v1/identity/account/profile", new
        {
            displayName = "Language Test",
            preferredLanguage = "   ",
        });
        Assert.Equal(HttpStatusCode.OK, blank.StatusCode);
        Assert.Null((await blank.Content.ReadFromJsonAsync<AccountResponse>())!.PreferredLanguage);

        var automatic = await client.PutAsJsonAsync("/api/v1/identity/account/profile", new
        {
            displayName = "Language Test",
            preferredLanguage = "AUTOMATIC",
        });
        Assert.Equal(HttpStatusCode.OK, automatic.StatusCode);
        Assert.Null((await automatic.Content.ReadFromJsonAsync<AccountResponse>())!.PreferredLanguage);

        var norwegian = await client.PutAsJsonAsync("/api/v1/identity/account/profile", new
        {
            displayName = "Language Test",
            preferredLanguage = "NB",
        });
        Assert.Equal(HttpStatusCode.OK, norwegian.StatusCode);
        Assert.Equal("nb", (await norwegian.Content.ReadFromJsonAsync<AccountResponse>())!.PreferredLanguage);

        var roundTrip = await client.GetFromJsonAsync<AccountResponse>("/api/v1/identity/account");
        Assert.Equal("nb", roundTrip!.PreferredLanguage);

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.Equal("nb", (await db.Users.SingleAsync(user => user.Id == credentials.Id)).PreferredLanguage);
    }

    [Fact]
    public async Task PasswordChange_InvalidPasswordDoesNotMutateAndReturnsValidation()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        var invalid = await client.PostAsJsonAsync("/api/v1/identity/account/password", new
        {
            currentPassword = "wrong-current-password",
            newPassword = "NewIntegrationPassword123",
        });
        Assert.Equal(HttpStatusCode.BadRequest, invalid.StatusCode);
        Assert.Equal("reauthentication_required", (await invalid.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);

        using var login = await factory.CreateAntiforgeryClientAsync();
        var stillWorks = await login.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = credentials.Email,
            password = credentials.Password,
        });
        Assert.Equal(HttpStatusCode.OK, stillWorks.StatusCode);
    }

    [Fact]
    public async Task Avatar_IsPrivateValidatedAndDeletedWithoutCrossUserAccess()
    {
        var first = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var second = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var firstClient = await factory.CreateAuthenticatedClientAsync(first.Email, first.Password);
        using var secondClient = await factory.CreateAuthenticatedClientAsync(second.Email, second.Password);
        using var owner = await factory.CreateOwnerClientAsync();

        using var upload = new MultipartFormDataContent();
        var imageBytes = Convert.FromBase64String(
            "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=");
        var image = new ByteArrayContent(imageBytes);
        image.Headers.ContentType = new MediaTypeHeaderValue("image/png");
        upload.Add(image, "avatar", "avatar.png");
        var uploaded = await firstClient.PutAsync("/api/v1/identity/account/avatar", upload);
        Assert.Equal(HttpStatusCode.OK, uploaded.StatusCode);

        var profile = await firstClient.GetFromJsonAsync<AccountResponse>("/api/v1/identity/account");
        Assert.Equal("/api/v1/identity/account/avatar", profile!.AvatarUrl);
        var avatar = await firstClient.GetAsync(profile.AvatarUrl);
        Assert.Equal(HttpStatusCode.OK, avatar.StatusCode);
        Assert.Contains("no-store", avatar.Headers.CacheControl?.ToString(), StringComparison.OrdinalIgnoreCase);
        Assert.Equal("nosniff", avatar.Headers.GetValues("X-Content-Type-Options").Single());

        var users = await owner.GetFromJsonAsync<ManagedUserResponse[]>("/api/v1/identity/owner/users");
        var listedFirst = Assert.Single(users!, user => user.Id == first.Id);
        Assert.Equal($"/api/v1/identity/owner/users/{first.Id}/avatar", listedFirst.AvatarUrl);
        var managedAvatar = await owner.GetAsync(listedFirst.AvatarUrl);
        Assert.Equal(HttpStatusCode.OK, managedAvatar.StatusCode);
        Assert.Equal("image/png", managedAvatar.Content.Headers.ContentType?.MediaType);
        Assert.Equal(imageBytes, await managedAvatar.Content.ReadAsByteArrayAsync());

        Assert.Equal(HttpStatusCode.Forbidden,
            (await secondClient.GetAsync($"/api/v1/identity/owner/users/{first.Id}/avatar")).StatusCode);
        Assert.Equal(HttpStatusCode.NotFound,
            (await owner.GetAsync($"/api/v1/identity/owner/users/{second.Id}/avatar")).StatusCode);

        Assert.Equal(HttpStatusCode.NotFound,
            (await secondClient.GetAsync("/api/v1/identity/account/avatar")).StatusCode);

        using var malformed = new MultipartFormDataContent();
        var malformedImage = new ByteArrayContent("not-an-image"u8.ToArray());
        malformedImage.Headers.ContentType = new MediaTypeHeaderValue("image/png");
        malformed.Add(malformedImage, "avatar", "avatar.png");
        Assert.Equal(HttpStatusCode.BadRequest,
            (await firstClient.PutAsync("/api/v1/identity/account/avatar", malformed)).StatusCode);

        using var svg = new MultipartFormDataContent();
        var svgImage = new ByteArrayContent("<svg xmlns='http://www.w3.org/2000/svg'></svg>"u8.ToArray());
        svgImage.Headers.ContentType = new MediaTypeHeaderValue("image/svg+xml");
        svg.Add(svgImage, "avatar", "avatar.svg");
        Assert.Equal(HttpStatusCode.BadRequest,
            (await firstClient.PutAsync("/api/v1/identity/account/avatar", svg)).StatusCode);

        using var oversized = new MultipartFormDataContent();
        var oversizedImage = new ByteArrayContent(new byte[(5 * 1024 * 1024) + 1]);
        oversizedImage.Headers.ContentType = new MediaTypeHeaderValue("image/png");
        oversized.Add(oversizedImage, "avatar", "avatar.png");
        Assert.Equal(HttpStatusCode.BadRequest,
            (await firstClient.PutAsync("/api/v1/identity/account/avatar", oversized)).StatusCode);

        Assert.Equal(HttpStatusCode.NoContent,
            (await firstClient.DeleteAsync("/api/v1/identity/account/avatar")).StatusCode);
        Assert.Equal(HttpStatusCode.NotFound,
            (await firstClient.GetAsync("/api/v1/identity/account/avatar")).StatusCode);
    }

    private sealed record AccountResponse(Guid Id, string DisplayName, string? Email, string? PreferredLanguage, string? AvatarUrl);

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
        bool TwoFactorEnabled,
        string? AvatarUrl,
        bool SsoEnabled);

}