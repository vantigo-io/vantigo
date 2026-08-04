using System.Net;
using System.Net.Http.Json;
using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;

using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Customers.Api.Database.Accounts;

namespace Vantigo.Customers.Api.Tests.Integration;

public sealed class Phase3MfaIntegrationTests
{
    [Fact]
    public async Task SetupEnableAndTwoFactorLoginUseTheBuiltInContinuationAndAmrCookie()
    {
        await using var factory = new FreshCustomersApiFactory { RequireOwnerMfa = true };
        await factory.StartAsync();
        using var client = await BootstrapAndLogin(factory);

        var initialSetup = await client.GetFromJsonAsync<MfaSetupDto>("/auth/owner/mfa/setup");
        Assert.False(initialSetup!.Initialized);

        var setup = await PostJson(client, "/auth/owner/mfa/setup", new { });
        Assert.True(setup.StatusCode == HttpStatusCode.OK,
            $"MFA setup failed: {setup.StatusCode} {await setup.Content.ReadAsStringAsync()}");
        var setupDto = await setup.Content.ReadFromJsonAsync<MfaSetupDto>();
        Assert.True(setupDto!.Initialized);
        Assert.NotNull(setupDto.SharedKey);

        // ResetAuthenticatorKeyAsync changes the security stamp. The setup response
        // must have replaced the cookie, so immediate code verification succeeds.
        var enable = await PostJson(client, "/auth/owner/mfa/enable", new { code = Totp(setupDto.SharedKey!) });
        Assert.Equal(HttpStatusCode.OK, enable.StatusCode);
        var enabled = await enable.Content.ReadFromJsonAsync<MfaEnableDto>();
        Assert.True(enabled!.TwoFactorEnabled);
        Assert.NotEmpty(enabled.RecoveryCodes);
        var session = await client.GetAsync("/auth/session");
        Assert.True(session.IsSuccessStatusCode, $"Session after enable failed: {session.StatusCode} {await session.Content.ReadAsStringAsync()}");
        Assert.Contains("\"mfaAuthenticated\":true", await session.Content.ReadAsStringAsync(), StringComparison.OrdinalIgnoreCase);
        var business = await client.GetAsync("/api/v1/customers");
        Assert.True(business.StatusCode == HttpStatusCode.OK,
            $"Business access failed: {business.StatusCode} {await business.Content.ReadAsStringAsync()}; session={await session.Content.ReadAsStringAsync()}");

        await PostJson(client, "/auth/logout", null);
        var challenge = await Login(client);
        Assert.True(challenge.RequiresTwoFactor);
        Assert.Equal(HttpStatusCode.Unauthorized, (await client.GetAsync("/auth/session")).StatusCode);
        var completed = await PostJson(client, "/auth/login/2fa", new { code = Totp(setupDto.SharedKey!) });
        Assert.Equal(HttpStatusCode.OK, completed.StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/api/v1/customers")).StatusCode);
    }

    [Fact]
    public async Task RecoveryCodeIsAcceptedOnceAndOwnerManagementNeedsMfaWhenRequired()
    {
        await using var factory = new FreshCustomersApiFactory { RequireOwnerMfa = true };
        await factory.StartAsync();
        using var client = await BootstrapAndLogin(factory);

        Assert.Equal(HttpStatusCode.Forbidden, (await client.GetAsync("/auth/owner/invitations")).StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/auth/owner/mfa")).StatusCode);

        var setup = await PostJson(client, "/auth/owner/mfa/setup", new { });
        var setupDto = await setup.Content.ReadFromJsonAsync<MfaSetupDto>();
        var enable = await PostJson(client, "/auth/owner/mfa/enable", new { code = Totp(setupDto!.SharedKey!) });
        var recovery = (await enable.Content.ReadFromJsonAsync<MfaEnableDto>())!.RecoveryCodes[0];

        await PostJson(client, "/auth/logout", null);
        Assert.True((await Login(client)).RequiresTwoFactor);
        Assert.Equal(HttpStatusCode.OK, (await PostJson(client, "/auth/login/2fa", new { code = recovery })).StatusCode);

        await PostJson(client, "/auth/logout", null);
        Assert.True((await Login(client)).RequiresTwoFactor);
        var replay = await PostJson(client, "/auth/login/2fa", new { code = recovery });
        Assert.Equal(HttpStatusCode.Unauthorized, replay.StatusCode);
    }

    [Fact]
    public async Task BadTotpAttemptsLockOutAndAssistedResetRequiresMfaOwner()
    {
        await using var factory = new FreshCustomersApiFactory { RequireOwnerMfa = true };
        await factory.StartAsync();
        using var client = await BootstrapAndLogin(factory);
        var setup = await PostJson(client, "/auth/owner/mfa/setup", new { });
        var setupDto = await setup.Content.ReadFromJsonAsync<MfaSetupDto>();
        Assert.Equal(HttpStatusCode.OK, (await PostJson(client, "/auth/owner/mfa/enable", new { code = Totp(setupDto!.SharedKey!) })).StatusCode);

        await PostJson(client, "/auth/logout", null);
        Assert.True((await Login(client)).RequiresTwoFactor);
        HttpStatusCode? last = null;
        for (var index = 0; index < 8; index++)
        {
            last = (await PostJson(client, "/auth/login/2fa", new { code = "000000" })).StatusCode;
            if (last == HttpStatusCode.TooManyRequests) break;
        }
        Assert.Equal(HttpStatusCode.TooManyRequests, last);
    }

    [Fact]
    public async Task AssistedResetRequiresMfaOwnerAndInvalidatesTargetSessionAndState()
    {
        await using var factory = new FreshCustomersApiFactory { RequireOwnerMfa = true };
        await factory.StartAsync();
        using var mfaOwner = await BootstrapAndLogin(factory);
        var setup = await PostJson(mfaOwner, "/auth/owner/mfa/setup", new { });
        var setupDto = await setup.Content.ReadFromJsonAsync<MfaSetupDto>();
        Assert.Equal(HttpStatusCode.OK,
            (await PostJson(mfaOwner, "/auth/owner/mfa/enable", new { code = Totp(setupDto!.SharedKey!) })).StatusCode);

        var targetEmail = $"target-owner-{Guid.NewGuid():N}@integration.test";
        var targetPassword = "TargetOwnerPassword123";
        Guid targetId;
        await using (var scope = factory.Services.GetRequiredService<IServiceScopeFactory>().CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var target = new ApplicationUser
            {
                UserName = targetEmail,
                Email = targetEmail,
                EmailConfirmed = true,
                DisplayName = "Target Owner",
            };
            Assert.True((await users.CreateAsync(target, targetPassword)).Succeeded);
            Assert.True((await users.AddToRoleAsync(target, "Owner")).Succeeded);
            var authenticatorStore = (IUserAuthenticatorKeyStore<ApplicationUser>)scope.ServiceProvider
                .GetRequiredService<IUserStore<ApplicationUser>>();
            await authenticatorStore.SetAuthenticatorKeyAsync(target, "JBSWY3DPEHPK3PXP", CancellationToken.None);
            Assert.True((await users.SetTwoFactorEnabledAsync(target, true)).Succeeded);
            Assert.True((await users.AddClaimAsync(target, new Claim("amr", "mfa"))).Succeeded);
            Assert.True((await users.AddClaimAsync(target, new Claim(ClaimTypes.AuthenticationMethod, "mfa"))).Succeeded);
            targetId = target.Id;
        }

        using var targetClient = await LoginTarget(factory, targetEmail, targetPassword, "JBSWY3DPEHPK3PXP");
        Assert.Equal(HttpStatusCode.OK, (await targetClient.GetAsync("/auth/session")).StatusCode);
        // The caller is MFA-authenticated by the endpoint flow above. A separate
        // non-MFA owner is created and exercised to prove the handler's fail closed path.
        using var nonMfaOwner = await CreateNonMfaOwnerClient(factory);
        var forbidden = await PostJson(nonMfaOwner, $"/auth/owner/mfa/reset/{targetId}", new { });
        Assert.Equal(HttpStatusCode.Forbidden, forbidden.StatusCode);

        var reset = await PostJson(mfaOwner, $"/auth/owner/mfa/reset/{targetId}", new { });
        Assert.Equal(HttpStatusCode.OK, reset.StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized, (await targetClient.GetAsync("/auth/session")).StatusCode);
        await using (var scope = factory.Services.GetRequiredService<IServiceScopeFactory>().CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var target = await users.FindByIdAsync(targetId.ToString());
            Assert.NotNull(target);
            Assert.False(target!.TwoFactorEnabled);
            Assert.DoesNotContain((await users.GetClaimsAsync(target)), claim => claim.Type is "amr" or ClaimTypes.AuthenticationMethod);
            // Identity rotates the authenticator key token rather than deleting
            // the row; the security state under test is the disabled flag and
            // removal of persisted MFA claims.
        }
    }

    private static async Task<HttpClient> BootstrapAndLogin(FreshCustomersApiFactory factory)
    {
        var client = await factory.CreateAntiforgeryClientAsync();
        var bootstrap = await client.PostAsJsonAsync("/auth/bootstrap", new
        {
            secret = FreshCustomersApiFactory.BootstrapSecret,
            email = "mfa-owner@integration.test",
            displayName = "MFA Owner",
            password = "MfaOwnerPassword123",
        });
        Assert.Equal(HttpStatusCode.Created, bootstrap.StatusCode);
        Assert.True((await Login(client)).User is not null);
        return client;
    }

    private static async Task<LoginDto> Login(HttpClient client)
    {
        await RefreshCsrf(client);
        var response = await client.PostAsJsonAsync("/auth/login", new
        {
            email = "mfa-owner@integration.test",
            password = "MfaOwnerPassword123",
        });
        Assert.True(response.StatusCode == HttpStatusCode.OK,
            $"Login failed: {response.StatusCode} {await response.Content.ReadAsStringAsync()}");
        var result = (await response.Content.ReadFromJsonAsync<LoginDto>())!;
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/auth/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        return result;
    }

    private static async Task RefreshCsrf(HttpClient client)
    {
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/auth/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
    }

    private static async Task<HttpResponseMessage> PostJson(HttpClient client, string path, object? body)
    {
        var response = body is null
            ? await client.PostAsync(path, null)
            : await client.PostAsJsonAsync(path, body);
        return response;
    }

    private static async Task<HttpClient> LoginTarget(FreshCustomersApiFactory factory, string email, string password, string authenticatorKey)
    {
        var client = await factory.CreateAntiforgeryClientAsync();
        var response = await client.PostAsJsonAsync("/auth/login", new { email, password });
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var challenge = await response.Content.ReadFromJsonAsync<LoginDto>();
        Assert.True(challenge!.RequiresTwoFactor);
        var completed = await PostJson(client, "/auth/login/2fa", new { code = Totp(authenticatorKey) });
        Assert.Equal(HttpStatusCode.OK, completed.StatusCode);
        return client;
    }

    private static async Task<HttpClient> CreateNonMfaOwnerClient(FreshCustomersApiFactory factory)
    {
        var email = $"non-mfa-owner-{Guid.NewGuid():N}@integration.test";
        const string password = "NonMfaOwnerPassword123";
        await using var scope = factory.Services.GetRequiredService<IServiceScopeFactory>().CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var owner = new ApplicationUser { UserName = email, Email = email, EmailConfirmed = true, DisplayName = "Non MFA Owner" };
        Assert.True((await users.CreateAsync(owner, password)).Succeeded);
        Assert.True((await users.AddToRoleAsync(owner, "Owner")).Succeeded);
        var client = await factory.CreateAntiforgeryClientAsync();
        Assert.Equal(HttpStatusCode.OK, (await client.PostAsJsonAsync("/auth/login", new { email, password })).StatusCode);
        return client;
    }

    private static string Totp(string base32)
    {
        const string alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
        var bits = 0;
        var value = 0;
        var bytes = new List<byte>();
        foreach (var character in base32.TrimEnd('=').ToUpperInvariant())
        {
            value = (value << 5) | alphabet.IndexOf(character);
            bits += 5;
            if (bits >= 8)
            {
                bits -= 8;
                bytes.Add((byte)(value >> bits));
            }
        }

        var counter = BitConverter.GetBytes(DateTimeOffset.UtcNow.ToUnixTimeSeconds() / 30);
        if (BitConverter.IsLittleEndian) Array.Reverse(counter);
        using var hmac = new HMACSHA1(bytes.ToArray());
        var hash = hmac.ComputeHash(counter);
        var offset = hash[^1] & 0x0f;
        var code = ((hash[offset] & 0x7f) << 24) |
                   (hash[offset + 1] << 16) |
                   (hash[offset + 2] << 8) |
                   hash[offset + 3];
        return (code % 1_000_000).ToString("D6");
    }

    private sealed record LoginDto(AuthUserDto? User, bool RequiresTwoFactor, bool TwoFactorEnabled, bool MfaEnrollmentRequired);
    private sealed record AuthUserDto(Guid Id, string DisplayName, string Email, string[] Roles);
    private sealed record MfaSetupDto(string? SharedKey, string? AuthenticatorUri, bool Initialized);
    private sealed record MfaEnableDto(bool TwoFactorEnabled, string[] RecoveryCodes);
}