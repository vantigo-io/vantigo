using System.Net;
using System.Net.Http.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Customers.Api.Database.Accounts;
using Vantigo.Customers.Api.Endpoints.Auth;

namespace Vantigo.Customers.Api.Tests.Integration;

[Collection("FreshCustomersApi")]
public sealed class WorkforceOidcCompletionTests
{
    [Fact]
    public async Task ValidExternalIdentity_JitProvisionsOnlyStandardUserAndReusesIssuerSubject()
    {
        await using var factory = new FreshCustomersApiFactory { EnableWorkforceOidc = true };
        await factory.StartAsync();
        var subject = $"subject-{Guid.NewGuid():N}";
        var email = $"workforce-{Guid.NewGuid():N}@integration.test";

        using var first = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });
        var firstResult = await Complete(first, subject, email);
        Assert.Equal(HttpStatusCode.Redirect, firstResult.StatusCode);
        Assert.Equal("/", firstResult.Headers.Location?.OriginalString);

        Guid userId;
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var user = await users.FindByEmailAsync(email);
            Assert.NotNull(user);
            userId = user!.Id;
            Assert.Equal("Workforce User", user.DisplayName);
            Assert.Contains(AuthRoles.User, await users.GetRolesAsync(user));
            Assert.DoesNotContain(AuthRoles.Owner, await users.GetRolesAsync(user));
            Assert.Single(await users.GetLoginsAsync(user));
        }

        using var second = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });
        var secondResult = await Complete(second, subject, email);
        Assert.Equal(HttpStatusCode.Redirect, secondResult.StatusCode);

        await using var finalScope = factory.Services.CreateAsyncScope();
        var db = finalScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.Equal(1, await db.Users.CountAsync(user => user.Id == userId));
        Assert.Equal(1, await db.UserLogins.CountAsync(login => login.UserId == userId));
    }

    [Fact]
    public async Task ExistingEmailWithoutIssuerSubjectLink_IsRejectedWithoutAutoLinking()
    {
        await using var factory = new FreshCustomersApiFactory { EnableWorkforceOidc = true };
        await factory.StartAsync();
        var email = $"existing-{Guid.NewGuid():N}@integration.test";
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var user = new ApplicationUser { UserName = email, Email = email, DisplayName = "Existing Local" };
            Assert.True((await users.CreateAsync(user, "ExistingPassword123")).Succeeded);
            var role = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>();
            Assert.True((await role.CreateAsync(new IdentityRole<Guid>(AuthRoles.User))).Succeeded);
            Assert.True((await users.AddToRoleAsync(user, AuthRoles.User)).Succeeded);
        }

        using var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });
        var response = await Complete(client, $"unlinked-{Guid.NewGuid():N}", email);

        Assert.Equal(HttpStatusCode.Redirect, response.StatusCode);
        Assert.Equal("/sign-in?error=oidc_email_conflict", response.Headers.Location?.OriginalString);
        await using var finalScope = factory.Services.CreateAsyncScope();
        var db = finalScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.Equal(0, await db.UserLogins.CountAsync(login => login.ProviderKey.StartsWith("unlinked-")));
    }

    [Fact]
    public async Task MissingEmail_UsesOpaqueNonDeliverableEmailAndDoesNotExposeIt()
    {
        await using var factory = new FreshCustomersApiFactory { EnableWorkforceOidc = true };
        await factory.StartAsync();
        var subject = $"no-email-{Guid.NewGuid():N}";
        using var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });

        var response = await CompleteWithoutEmail(client, subject);
        Assert.Equal(HttpStatusCode.Redirect, response.StatusCode);
        Assert.Equal("/", response.Headers.Location?.OriginalString);

        await using var scope = factory.Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = await users.Users.SingleAsync(item => item.UserName!.StartsWith("oidc-"));
        Assert.False(user.EmailConfirmed);
        Assert.NotNull(user.Email);
        Assert.True(WorkforceOidcOptions.IsOpaqueEmail(user.Email));
        Assert.Equal(HttpStatusCode.OK, (await client.GetAsync("/auth/session")).StatusCode);
        var session = await client.GetFromJsonAsync<SessionDto>("/auth/session");
        Assert.Null(session!.User.Email);
    }

    [Fact]
    public async Task SubjectComparison_RemainsCaseSensitive()
    {
        await using var factory = new FreshCustomersApiFactory { EnableWorkforceOidc = true };
        await factory.StartAsync();
        using var first = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });
        using var second = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });

        Assert.Equal("/", (await Complete(first, "CaseSensitive", $"case-one-{Guid.NewGuid():N}@integration.test")).Headers.Location?.OriginalString);
        Assert.Equal("/", (await Complete(second, "casesensitive", $"case-two-{Guid.NewGuid():N}@integration.test")).Headers.Location?.OriginalString);

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        Assert.Equal(2, await db.UserLogins.CountAsync(login => login.LoginProvider == "https://issuer.integration.test"));
        Assert.Equal(2, await db.Users.CountAsync(user => user.UserName!.StartsWith("oidc-")));
    }

    [Fact]
    public async Task DedicatedIssuerClaimMismatch_IsRejected()
    {
        await using var factory = new FreshCustomersApiFactory { EnableWorkforceOidc = true };
        await factory.StartAsync();
        using var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });

        var response = await client.GetAsync(
            $"/test/oidc-external?issuer={Uri.EscapeDataString("https://other-issuer.integration.test")}&sub=mismatch");
        Assert.Equal(WorkforceOidcOptions.CompletionPath, response.Headers.Location?.OriginalString);
        var completion = await client.GetAsync(response.Headers.Location);

        Assert.Equal(HttpStatusCode.Redirect, completion.StatusCode);
        Assert.Equal("/sign-in?error=oidc_identity_invalid", completion.Headers.Location?.OriginalString);
    }

    [Fact]
    public async Task MissingSubject_IsRejectedWithoutProvisioning()
    {
        await using var factory = new FreshCustomersApiFactory { EnableWorkforceOidc = true };
        await factory.StartAsync();
        using var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });

        var response = await client.GetAsync("/test/oidc-external?sub=%20&email=missing-subject%40integration.test");

        // Empty query values are omitted by the controlled producer's fallback,
        // so use a whitespace subject to exercise the required-claim branch.
        if (response.StatusCode == HttpStatusCode.Redirect &&
            response.Headers.Location?.OriginalString == WorkforceOidcOptions.CompletionPath)
        {
            response = await client.GetAsync(response.Headers.Location);
        }

        Assert.Equal(HttpStatusCode.Redirect, response.StatusCode);
        Assert.Equal("/sign-in?error=oidc_identity_invalid", response.Headers.Location?.OriginalString);
    }

    private static async Task<HttpResponseMessage> Complete(HttpClient client, string subject, string email)
    {
        var response = await client.GetAsync($"/test/oidc-external?sub={Uri.EscapeDataString(subject)}&email={Uri.EscapeDataString(email)}");
        Assert.Equal(HttpStatusCode.Redirect, response.StatusCode);
        Assert.Equal(WorkforceOidcOptions.CompletionPath, response.Headers.Location?.OriginalString);
        return await client.GetAsync(response.Headers.Location);
    }

    private static async Task<HttpResponseMessage> CompleteWithoutEmail(HttpClient client, string subject)
    {
        var response = await client.GetAsync($"/test/oidc-external?sub={Uri.EscapeDataString(subject)}");
        Assert.Equal(WorkforceOidcOptions.CompletionPath, response.Headers.Location?.OriginalString);
        return await client.GetAsync(response.Headers.Location);
    }

    private sealed record SessionDto(SessionUser User);
    private sealed record SessionUser(Guid Id, string DisplayName, string? Email, string[] Roles);
}
