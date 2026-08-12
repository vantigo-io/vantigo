using System.Net;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityOidcApiCollection.Name)]
public sealed class IdentityOidcIntegrationTests(OidcIdentityApiFactory factory)
{
    [Fact]
    public async Task DisabledExistingOidcAccount_IsRejectedWithGenericAccountLockedError()
    {
        var subject = $"disabled-oidc-{Guid.NewGuid():N}";
        var email = $"disabled-oidc-{Guid.NewGuid():N}@integration.test";
        using var first = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });

        var provision = await CompleteExternalAsync(first, subject, email);
        Assert.Equal("/", provision.Headers.Location?.OriginalString);

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            var user = await db.Users.SingleAsync(item => item.Email == email);
            user.IsDisabled = true;
            await db.SaveChangesAsync();
        }

        using var second = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true,
        });
        var rejected = await CompleteExternalAsync(second, subject, email);

        Assert.Equal(HttpStatusCode.Redirect, rejected.StatusCode);
        Assert.Equal("/sign-in?error=account_locked", rejected.Headers.Location?.OriginalString);
    }

    private static async Task<HttpResponseMessage> CompleteExternalAsync(
        HttpClient client,
        string subject,
        string email)
    {
        var external = await client.GetAsync(
            $"/test/oidc-external?sub={Uri.EscapeDataString(subject)}&email={Uri.EscapeDataString(email)}");
        Assert.Equal(WorkforceOidcOptions.CompletionPath, external.Headers.Location?.OriginalString);
        return await client.GetAsync(external.Headers.Location);
    }
}