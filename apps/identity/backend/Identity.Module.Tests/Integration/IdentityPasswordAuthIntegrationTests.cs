using System.Net;
using System.Net.Http.Json;
using System.Text.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentityPasswordAuthIntegrationTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task LocalPasswordLoginEstablishesSessionAndLogoutClearsIt()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var client = await factory.CreateAntiforgeryClientAsync();

        var login = await client.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = credentials.Email,
            password = credentials.Password,
        });

        Assert.True(login.IsSuccessStatusCode,
            $"Expected successful login, got {login.StatusCode}: {await login.Content.ReadAsStringAsync()}");
        Assert.Equal(credentials.Email, (await login.Content.ReadFromJsonAsync<JsonElement>())
            .GetProperty("user").GetProperty("email").GetString());
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/api/v1/identity/session")).StatusCode);

        await IdentityApiFactory.RefreshAntiforgeryAsync(client);
        var logout = await client.PostAsync("/api/v1/identity/logout", null);

        Assert.Equal(HttpStatusCode.OK, logout.StatusCode);
        Assert.True((await logout.Content.ReadFromJsonAsync<JsonElement>()).GetProperty("success").GetBoolean());
        Assert.Equal(HttpStatusCode.Unauthorized, (await client.GetAsync("/api/v1/identity/session")).StatusCode);
    }

    [Fact]
    public async Task UnknownEmailAndIncorrectPasswordReturnIndistinguishableResponsesAndNoSession()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var unknownClient = await factory.CreateAntiforgeryClientAsync();
        using var incorrectClient = await factory.CreateAntiforgeryClientAsync();

        var unknown = await unknownClient.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = $"unknown-{Guid.NewGuid():N}@integration.test",
            password = credentials.Password,
        });
        var incorrect = await incorrectClient.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = credentials.Email,
            password = "WrongPassword123",
        });

        Assert.Equal(HttpStatusCode.Unauthorized, unknown.StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized, incorrect.StatusCode);
        var unknownError = await unknown.Content.ReadFromJsonAsync<ErrorResponse>();
        var incorrectError = await incorrect.Content.ReadFromJsonAsync<ErrorResponse>();
        Assert.NotNull(unknownError);
        Assert.NotNull(incorrectError);
        Assert.Equal(unknownError!.Error.Code, incorrectError!.Error.Code);
        Assert.Equal("invalid_credentials", unknownError.Error.Code);
        Assert.Equal(unknownError.Error.Message, incorrectError.Error.Message);
        Assert.Equal(HttpStatusCode.Unauthorized, (await unknownClient.GetAsync("/api/v1/identity/session")).StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized, (await incorrectClient.GetAsync("/api/v1/identity/session")).StatusCode);
    }

    [Fact]
    public async Task PasswordRecoveryReturnsIndistinguishableResponsesAndEmailsOnlyEligibleAccount()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var unknownEmail = $"unknown-recovery-{Guid.NewGuid():N}@integration.test";
        var unconfirmedEmail = await CreateDirectAccountAsync(emailConfirmed: false, withPassword: true);
        var passwordlessEmail = await CreateDirectAccountAsync(emailConfirmed: true, withPassword: false);
        foreach (var email in new[] { credentials.Email, unknownEmail, unconfirmedEmail, passwordlessEmail })
        {
            factory.EmailSender.ClearFor(email);
        }

        using var knownClient = await factory.CreateAntiforgeryClientAsync();
        using var unknownClient = await factory.CreateAntiforgeryClientAsync();
        using var unconfirmedClient = await factory.CreateAntiforgeryClientAsync();
        using var passwordlessClient = await factory.CreateAntiforgeryClientAsync();
        var known = await knownClient.PostAsJsonAsync("/api/v1/identity/password-recovery/request", new
        {
            email = credentials.Email,
        });
        var unknown = await unknownClient.PostAsJsonAsync("/api/v1/identity/password-recovery/request", new
        {
            email = unknownEmail,
        });
        var unconfirmed = await unconfirmedClient.PostAsJsonAsync("/api/v1/identity/password-recovery/request", new
        {
            email = unconfirmedEmail,
        });
        var passwordless = await passwordlessClient.PostAsJsonAsync("/api/v1/identity/password-recovery/request", new
        {
            email = passwordlessEmail,
        });

        var responses = new[] { known, unknown, unconfirmed, passwordless };
        Assert.All(responses, response => Assert.Equal(HttpStatusCode.OK, response.StatusCode));
        var responseBodies = await Task.WhenAll(responses.Select(response => response.Content.ReadAsStringAsync()));
        Assert.All(responseBodies, body => Assert.Equal(responseBodies[0], body));
        Assert.True((await known.Content.ReadFromJsonAsync<JsonElement>()).GetProperty("accepted").GetBoolean());
        Assert.Single(factory.EmailSender.ReadFor(credentials.Email));
        Assert.Empty(factory.EmailSender.ReadFor(unknownEmail));
        Assert.Empty(factory.EmailSender.ReadFor(unconfirmedEmail));
        Assert.Empty(factory.EmailSender.ReadFor(passwordlessEmail));
    }

    [Fact]
    public async Task UnsafePasswordEndpointsRejectMissingAntiforgeryHeader()
    {
        using var loginClient = await factory.CreateAntiforgeryClientAsync();
        loginClient.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var login = await loginClient.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = "csrf-dummy@integration.test",
            password = "csrf-dummy-password",
        });
        AssertCsrfFailure(login);

        using var recoveryClient = await factory.CreateAntiforgeryClientAsync();
        recoveryClient.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var recovery = await recoveryClient.PostAsJsonAsync("/api/v1/identity/password-recovery/request", new
        {
            email = "csrf-dummy@integration.test",
        });
        AssertCsrfFailure(recovery);

        using var resetClient = await factory.CreateAntiforgeryClientAsync();
        resetClient.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var reset = await resetClient.PostAsJsonAsync("/api/v1/identity/password-recovery/reset", new
        {
            email = "csrf-dummy@integration.test",
            token = "csrf-dummy-token",
            newPassword = "csrf-dummy-new-password",
        });
        AssertCsrfFailure(reset);

        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var logoutClient = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        logoutClient.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var logout = await logoutClient.PostAsync("/api/v1/identity/logout", null);
        AssertCsrfFailure(logout);
    }

    [Fact]
    public async Task PasswordRecoveryRoundTripChangesPasswordInvalidatesSessionAndRejectsReplay()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        var newPassword = "IntegrationNewPassword123";
        factory.EmailSender.ClearFor(credentials.Email);
        using var existingSession = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);
        using var requestClient = await factory.CreateAntiforgeryClientAsync();

        var request = await requestClient.PostAsJsonAsync("/api/v1/identity/password-recovery/request", new
        {
            email = credentials.Email,
        });
        Assert.Equal(HttpStatusCode.OK, request.StatusCode);

        var email = Assert.Single(factory.EmailSender.ReadFor(credentials.Email));
        var resetUrl = ExtractResetUrl(email.TextBody);
        var query = ParseQuery(resetUrl);
        Assert.Equal(credentials.Email, query["email"]);
        Assert.False(string.IsNullOrWhiteSpace(query["token"]));

        using var resetClient = await factory.CreateAntiforgeryClientAsync();
        var reset = await resetClient.PostAsJsonAsync("/api/v1/identity/password-recovery/reset", new
        {
            email = query["email"],
            token = query["token"],
            newPassword,
        });
        Assert.Equal(HttpStatusCode.OK, reset.StatusCode);
        Assert.True((await reset.Content.ReadFromJsonAsync<JsonElement>()).GetProperty("success").GetBoolean());
        Assert.Equal(HttpStatusCode.Unauthorized, (await existingSession.GetAsync("/api/v1/identity/session")).StatusCode);

        using var oldPasswordClient = await factory.CreateAntiforgeryClientAsync();
        var oldPassword = await oldPasswordClient.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = credentials.Email,
            password = credentials.Password,
        });
        Assert.Equal(HttpStatusCode.Unauthorized, oldPassword.StatusCode);
        Assert.Equal("invalid_credentials", (await oldPassword.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);

        using var newPasswordClient = await factory.CreateAntiforgeryClientAsync();
        var newPasswordLogin = await newPasswordClient.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = credentials.Email,
            password = newPassword,
        });
        Assert.Equal(HttpStatusCode.OK, newPasswordLogin.StatusCode);
        Assert.Equal(HttpStatusCode.OK, (await newPasswordClient.GetAsync("/api/v1/identity/session")).StatusCode);

        using var replayClient = await factory.CreateAntiforgeryClientAsync();
        var replay = await replayClient.PostAsJsonAsync("/api/v1/identity/password-recovery/reset", new
        {
            email = query["email"],
            token = query["token"],
            newPassword = "AnotherIntegrationPassword123",
        });
        Assert.Equal(HttpStatusCode.BadRequest, replay.StatusCode);
        Assert.Equal("invalid_reset_token", (await replay.Content.ReadFromJsonAsync<ErrorResponse>())!.Error.Code);
        Assert.Equal(HttpStatusCode.Unauthorized, (await existingSession.GetAsync("/api/v1/identity/session")).StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized, (await replayClient.GetAsync("/api/v1/identity/session")).StatusCode);

        using var replayPasswordClient = await factory.CreateAntiforgeryClientAsync();
        var replayPassword = await replayPasswordClient.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = credentials.Email,
            password = "AnotherIntegrationPassword123",
        });
        Assert.Equal(HttpStatusCode.Unauthorized, replayPassword.StatusCode);

        using var stillValidClient = await factory.CreateAntiforgeryClientAsync();
        var stillValid = await stillValidClient.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = credentials.Email,
            password = newPassword,
        });
        Assert.Equal(HttpStatusCode.OK, stillValid.StatusCode);
    }

    private static Uri ExtractResetUrl(string textBody)
    {
        var start = textBody.IndexOf("http", StringComparison.Ordinal);
        Assert.True(start >= 0);
        var url = textBody[start..].Trim();
        Assert.True(Uri.TryCreate(url, UriKind.Absolute, out var parsed));
        return parsed!;
    }

    private static Dictionary<string, string> ParseQuery(Uri uri) =>
        uri.Query.TrimStart('?').Split('&', StringSplitOptions.RemoveEmptyEntries)
            .Select(item => item.Split('=', 2))
            .ToDictionary(item => Uri.UnescapeDataString(item[0]),
                item => Uri.UnescapeDataString(item.Length == 2 ? item[1] : string.Empty));

    private async Task<string> CreateDirectAccountAsync(bool emailConfirmed, bool withPassword)
    {
        var email = $"direct-recovery-{Guid.NewGuid():N}@integration.test";
        var password = "DirectRecoveryPassword123";
        await using var scope = factory.Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = new ApplicationUser
        {
            UserName = email,
            Email = email,
            EmailConfirmed = emailConfirmed,
            DisplayName = "Direct Recovery Test User",
        };
        var result = withPassword
            ? await users.CreateAsync(user, password)
            : await users.CreateAsync(user);
        Assert.True(result.Succeeded,
            $"Could not create direct recovery account: {string.Join("; ", result.Errors.Select(error => error.Description))}");
        return email;
    }

    private static void AssertCsrfFailure(HttpResponseMessage response)
    {
        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var error = response.Content.ReadFromJsonAsync<ErrorResponse>().GetAwaiter().GetResult();
        Assert.Equal("csrf_validation_failed", error!.Error.Code);
    }

    private sealed record ErrorResponse(Error Error);
    private sealed record Error(string Code, string Message, Dictionary<string, string[]>? Fields = null);
}