using System.Net;
using System.Net.Http.Json;
using System.Text.Json;

using Vantigo.Contracts.Identity;

namespace Vantigo.Identity.Tests.Integration;

[Collection(IdentityApiCollection.Name)]
public sealed class IdentityMaintenanceModeIntegrationTests(IdentityApiFactory factory) : IAsyncLifetime
{
    public async Task InitializeAsync() => await factory.ResetIdentityStateAsync();

    public Task DisposeAsync() => Task.CompletedTask;

    [Fact]
    public async Task StatusIsAnonymousAndDefaultsToDisabled()
    {
        using var client = factory.CreateCookieClient();

        var response = await client.GetAsync("/api/v1/identity/system/status");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var status = await response.Content.ReadFromJsonAsync<JsonElement>();
        Assert.False(status.GetProperty("maintenance").GetBoolean());
        Assert.Equal(JsonValueKind.Null, status.GetProperty("message").ValueKind);
    }

    [Fact]
    public async Task NonSystemAdminCannotUpdateMaintenanceMode()
    {
        var credentials = await factory.CreateUserWithCredentialsAsync(AuthRoles.User);
        using var client = await factory.CreateAuthenticatedClientAsync(credentials.Email, credentials.Password);

        var response = await client.PutAsJsonAsync("/api/v1/identity/system/maintenance", new
        {
            enabled = true,
            message = "Denied",
        });

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task SystemAdminCanEnableAndDisableMaintenanceMode()
    {
        using var client = await factory.CreateOwnerClientAsync();

        var enabled = await client.PutAsJsonAsync("/api/v1/identity/system/maintenance", new
        {
            enabled = true,
            message = "Scheduled maintenance",
        });
        Assert.Equal(HttpStatusCode.OK, enabled.StatusCode);
        AssertStatus(await enabled.Content.ReadFromJsonAsync<JsonElement>(), true, "Scheduled maintenance");

        var status = await client.GetFromJsonAsync<JsonElement>("/api/v1/identity/system/status");
        AssertStatus(status, true, "Scheduled maintenance");

        var disabled = await client.PutAsJsonAsync("/api/v1/identity/system/maintenance", new
        {
            enabled = false,
            message = (string?)null,
        });
        Assert.Equal(HttpStatusCode.OK, disabled.StatusCode);
        AssertStatus(await disabled.Content.ReadFromJsonAsync<JsonElement>(), false, null);
        AssertStatus(await client.GetFromJsonAsync<JsonElement>("/api/v1/identity/system/status"), false, null);
    }

    [Fact]
    public async Task MessageOver500CharactersReturnsBadRequest()
    {
        using var client = await factory.CreateOwnerClientAsync();

        var response = await client.PutAsJsonAsync("/api/v1/identity/system/maintenance", new
        {
            enabled = true,
            message = new string('x', 501),
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    private static void AssertStatus(JsonElement status, bool maintenance, string? message)
    {
        Assert.Equal(maintenance, status.GetProperty("maintenance").GetBoolean());
        Assert.Equal(message, status.GetProperty("message").GetString());
    }
}