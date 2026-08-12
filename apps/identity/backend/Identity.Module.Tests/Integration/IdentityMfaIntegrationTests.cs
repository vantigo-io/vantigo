using System.Net;
using System.Net.Http.Json;
using System.Security.Claims;
using System.Text.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.WebUtilities;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityMfaApiCollection.Name)]
public sealed class IdentityMfaIntegrationTests(MfaIdentityApiFactory factory)
{
    [Fact]
    public async Task OwnerManagement_RequiresMfaWhenConfigured()
    {
        using var owner = await factory.CreateOwnerClientAsync();

        var response = await owner.GetAsync("/api/v1/identity/owner/users");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task AuthorizationManagement_RequiresMfaWhenConfigured()
    {
        using var owner = await factory.CreateOwnerClientAsync();

        var response = await owner.GetAsync("/api/v1/identity/access/roles");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task OrdinaryAuthenticatedUserCanEnrollMfaAndCrossUserResetRemainsDenied()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync("MfaDelegate");
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        var setup = await client.PostAsJsonAsync("/api/v1/identity/owner/mfa/setup", new { password = credentials.Password });
        Assert.Equal(HttpStatusCode.OK, setup.StatusCode);
        var setupBody = await setup.Content.ReadFromJsonAsync<SetupResponse>();
        Assert.NotNull(setupBody);
        var code = IdentityApiFactory.CreateTotpCode(setupBody!.SharedKey!);
        var enable = await client.PostAsJsonAsync("/api/v1/identity/owner/mfa/enable", new { code, password = credentials.Password });
        Assert.Equal(HttpStatusCode.OK, enable.StatusCode);

        await using (var claimsScope = factory.Services.CreateAsyncScope())
        {
            var claimUsers = claimsScope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var enrolled = await claimUsers.FindByIdAsync(credentials.Id.ToString());
            Assert.NotNull(enrolled);
            Assert.DoesNotContain(await claimUsers.GetClaimsAsync(enrolled!), IsMfaClaim);
        }

        await using var scope = factory.Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var owner = await users.FindByEmailAsync(IdentityApiFactory.OwnerEmail);
        Assert.NotNull(owner);
        var reset = await client.PostAsync($"/api/v1/identity/owner/mfa/reset/{owner!.Id}", null);
        Assert.Equal(HttpStatusCode.Forbidden, reset.StatusCode);
    }

    [Fact]
    public async Task AccountMfaAliasesRequirePasswordAndDoNotPersistMfaClaims()
    {
        using var owner = await factory.CreateOwnerClientAsync();

        var missingPassword = await owner.PostAsJsonAsync("/api/v1/identity/account/mfa/setup", new { });
        Assert.Equal(HttpStatusCode.BadRequest, missingPassword.StatusCode);
        Assert.Equal("reauthentication_required",
            (await missingPassword.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);

        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        var setup = await client.PostAsJsonAsync("/api/v1/identity/account/mfa/setup", new { password = credentials.Password });
        Assert.Equal(HttpStatusCode.OK, setup.StatusCode);
        var setupBody = await setup.Content.ReadFromJsonAsync<SetupResponse>();
        Assert.NotNull(setupBody?.SharedKey);

        var enable = await client.PostAsJsonAsync("/api/v1/identity/account/mfa/enable", new
        {
            code = IdentityApiFactory.CreateTotpCode(setupBody!.SharedKey!),
            password = credentials.Password,
        });
        Assert.Equal(HttpStatusCode.OK, enable.StatusCode);

        await using var scope = factory.Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = await users.FindByIdAsync(credentials.Id.ToString());
        Assert.NotNull(user);
        Assert.DoesNotContain(await users.GetClaimsAsync(user!), IsMfaClaim);
    }

    [Fact]
    public async Task PasskeyBeginRejectsLockedAccountWithGenericAvailabilityResponse()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync("PasskeyLocked");
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var user = await users.FindByIdAsync(credentials.Id.ToString());
            Assert.NotNull(user);
            var passkey = new UserPasskeyInfo(
                [1, 2, 3], [1], DateTimeOffset.UtcNow, 0, [], true, false, false, [], []);
            Assert.True((await users.AddOrUpdatePasskeyAsync(user!, passkey)).Succeeded);
            user!.LockoutEnd = DateTimeOffset.UtcNow.AddMinutes(10);
            Assert.True((await users.UpdateAsync(user)).Succeeded);
        }

        using var client = await factory.CreateAntiforgeryClientAsync();
        var response = await client.PostAsJsonAsync("/api/v1/identity/passkeys/login/begin", new { email = credentials.Email });
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        using var options = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
        Assert.True(options.RootElement.TryGetProperty("options", out _));
    }

    [Fact]
    public async Task PasskeyBeginDoesNotEnumerateUnknownOrNoPasskeyAccounts()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync("NoPasskey");
        using var client = await factory.CreateAntiforgeryClientAsync();

        var known = await client.PostAsJsonAsync("/api/v1/identity/passkeys/login/begin", new { email = credentials.Email });
        var unknown = await client.PostAsJsonAsync("/api/v1/identity/passkeys/login/begin", new
        {
            email = $"missing-{Guid.NewGuid():N}@integration.test",
        });

        Assert.Equal(HttpStatusCode.OK, known.StatusCode);
        Assert.Equal(HttpStatusCode.OK, unknown.StatusCode);
        using var knownJson = JsonDocument.Parse(await known.Content.ReadAsStringAsync());
        using var unknownJson = JsonDocument.Parse(await unknown.Content.ReadAsStringAsync());
        Assert.Equal(
            knownJson.RootElement.GetProperty("options").EnumerateObject().Select(property => property.Name).OrderBy(name => name),
            unknownJson.RootElement.GetProperty("options").EnumerateObject().Select(property => property.Name).OrderBy(name => name));
        Assert.False(knownJson.RootElement.GetProperty("options").TryGetProperty("allowCredentials", out _));
        Assert.False(unknownJson.RootElement.GetProperty("options").TryGetProperty("allowCredentials", out _));
    }

    [Fact]
    public async Task PasskeyCeremoniesAreRemovedAfterFailureAndExpiredRowsArePurged()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync("CeremonyCleanup");
        using var client = await factory.CreateAntiforgeryClientAsync();

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            db.PasskeyCeremonies.Add(new PasskeyCeremony
            {
                UserId = credentials.Id,
                Kind = "login",
                State = "expired",
                ClientAddress = "127.0.0.1",
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(-1),
            });
            await db.SaveChangesAsync();
        }

        var begin = await client.PostAsJsonAsync("/api/v1/identity/passkeys/login/begin", new { email = credentials.Email });
        Assert.Equal(HttpStatusCode.OK, begin.StatusCode);
        var ceremony = await begin.Content.ReadFromJsonAsync<PasskeyOptionsResponse>();
        Assert.NotNull(ceremony);

        var complete = await client.PostAsJsonAsync("/api/v1/identity/passkeys/login/complete", new
        {
            ceremonyId = ceremony!.CeremonyId,
            credentialJson = "{}",
        });
        Assert.Equal(HttpStatusCode.Unauthorized, complete.StatusCode);

        await using var verifyScope = factory.Services.CreateAsyncScope();
        var verifyDb = verifyScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.False(await verifyDb.PasskeyCeremonies.AnyAsync(item => item.Id == ceremony.CeremonyId, CancellationToken.None));

        var replay = await client.PostAsJsonAsync("/api/v1/identity/passkeys/login/complete", new
        {
            ceremonyId = ceremony.CeremonyId,
            credentialJson = "{}",
        });
        Assert.Equal(HttpStatusCode.Conflict, replay.StatusCode);
        Assert.False(await verifyDb.PasskeyCeremonies.AnyAsync(item => item.State == "expired", CancellationToken.None));
    }

    [Fact]
    public async Task TotpLoginMfaClaimIsCookieOnly()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync("MfaLogin");
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        var setup = await client.PostAsJsonAsync("/api/v1/identity/account/mfa/setup", new { password = credentials.Password });
        var setupBody = await setup.Content.ReadFromJsonAsync<SetupResponse>();
        Assert.NotNull(setupBody?.SharedKey);
        Assert.Equal(HttpStatusCode.OK, (await client.PostAsJsonAsync("/api/v1/identity/account/mfa/enable", new
        {
            code = IdentityApiFactory.CreateTotpCode(setupBody!.SharedKey!),
            password = credentials.Password,
        })).StatusCode);

        await client.PostAsync("/api/v1/identity/logout", null);
        await IdentityApiFactory.RefreshAntiforgeryAsync(client);
        var login = await client.PostAsJsonAsync("/api/v1/identity/login", new { email = credentials.Email, password = credentials.Password });
        Assert.Equal(HttpStatusCode.OK, login.StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await client.PostAsJsonAsync("/api/v1/identity/login/2fa", new
        {
            code = IdentityApiFactory.CreateTotpCode(setupBody.SharedKey!),
        })).StatusCode);

        await using var scope = factory.Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = await users.FindByIdAsync(credentials.Id.ToString());
        Assert.NotNull(user);
        Assert.DoesNotContain(await users.GetClaimsAsync(user!), IsMfaClaim);
    }

    [Fact]
    public async Task PasskeyRemovalAcceptsMaximumBoundedCredentialIdLength()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync("CredentialBound");
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        var maximumId = WebEncoders.Base64UrlEncode(new byte[1023]);
        Assert.Equal(1364, maximumId.Length);

        using var request = new HttpRequestMessage(
            HttpMethod.Delete,
            $"/api/v1/identity/account/passkeys/{maximumId}")
        {
            Content = JsonContent.Create(new { currentPassword = credentials.Password }),
        };
        var response = await client.SendAsync(request);

        // The identifier is accepted by the bounded decoder; no credential is
        // registered for this test user, so the endpoint returns NotFound.
        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    private static bool IsMfaClaim(Claim claim) =>
        (claim.Type == "amr" || claim.Type == ClaimTypes.AuthenticationMethod) &&
        string.Equals(claim.Value, "mfa", StringComparison.OrdinalIgnoreCase);

    private sealed record SetupResponse(string? SharedKey, string? AuthenticatorUri, bool Initialized);
    private sealed record ErrorResponse(Error Error);
    private sealed record Error(string Code, string Message, Dictionary<string, string[]>? Fields = null);
    private sealed record PasskeyOptionsResponse(Guid CeremonyId, JsonElement Options);
}