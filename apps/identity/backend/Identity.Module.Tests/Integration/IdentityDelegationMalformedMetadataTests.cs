using System.Net;
using System.Net.Http.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentityDelegationMalformedMetadataTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task MissingReferencedRoleMetadataFailsClosedForDelegatedManagement()
    {
        var delegateUser = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var owner = await factory.CreateOwnerClientAsync();
        var role = await CreateCustomRoleAsync(owner);
        var delegation = await owner.PostAsJsonAsync("/api/v1/identity/access/delegations", new
        {
            granteeUserId = delegateUser.Id,
            expiresAt = DateTimeOffset.UtcNow.AddHours(1),
            permissionKeys = Array.Empty<string>(),
            stewardedRoleIds = new[] { role.Id },
            canCreateRoles = true,
        });
        Assert.Equal(HttpStatusCode.Created, delegation.StatusCode);

        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
            await db.RoleMetadata.Where(item => item.RoleId == role.Id).ExecuteDeleteAsync();
        }

        using var delegated = await factory.CreateAuthenticatedClientAsync(delegateUser.Email, delegateUser.Password);
        var response = await delegated.GetAsync("/api/v1/identity/access/roles");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    private async Task<(Guid Id, string Version)> CreateCustomRoleAsync(HttpClient owner)
    {
        var name = $"rbac-malformed-{Guid.NewGuid():N}";
        var response = await owner.PostAsJsonAsync("/api/v1/identity/access/roles", new
        {
            name,
            displayName = name,
            description = "Malformed metadata test role",
            permissionKeys = Array.Empty<string>(),
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var body = await response.Content.ReadFromJsonAsync<RoleResponse>();
        Assert.NotNull(body);
        return (body!.Id, body.Version);
    }

    private sealed record RoleResponse(Guid Id, string Name, string Version);
}