using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text;
using System.Text.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

[Collection(StaticScimApiCollection.Name)]
public sealed class StaticScimProtocolIntegrationTests(StaticScimIdentityApiFactory factory) : IAsyncLifetime
{
    private static readonly string[] UserSchemas = [ScimProtocolService.UserSchema];
    private static readonly string[] GroupSchemas = [ScimProtocolService.GroupSchema];
    private static readonly string[] PatchSchemas = [ScimProtocolService.PatchSchema];

    public async Task InitializeAsync() => await factory.ResetIdentityStateAsync();
    public Task DisposeAsync() => Task.CompletedTask;

    [Fact]
    public async Task StaticTokenUsersGroupsEtagsLifecycleAndAuditRemainProtocolOnly()
    {
        using var invalid = CreateScimClient("not-the-static-token");
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await invalid.GetAsync(ScimProtocolService.BasePath + "/ServiceProviderConfig")).StatusCode);

        using var scim = CreateScimClient();
        var username = $"scim-{Guid.NewGuid():N}";
        var userResponse = await SendAsync(scim, HttpMethod.Post, "/Users", new
        {
            schemas = UserSchemas,
            externalId = Guid.NewGuid().ToString("D"),
            userName = username,
            active = true,
            displayName = "SCIM Protocol User",
            emails = new[] { new { value = $"{username}@integration.test", type = "work", primary = true } },
        });
        Assert.Equal(HttpStatusCode.Created, userResponse.StatusCode);
        Assert.NotNull(userResponse.Headers.ETag);
        var user = await userResponse.Content.ReadFromJsonAsync<JsonElement>();
        var userId = user.GetProperty("id").GetString()!;
        var initialUserEtag = userResponse.Headers.ETag!.Tag!.Trim('"');
        Assert.Equal(initialUserEtag, user.GetProperty("meta").GetProperty("version").GetString());

        var listed = await scim.GetFromJsonAsync<JsonElement>(ScimProtocolService.BasePath + "/Users?filter=" +
            Uri.EscapeDataString($"userName eq \"{username}\""));
        Assert.Equal(1, listed.GetProperty("totalResults").GetInt32());

        using var staleUserPatch = CreateScimRequest(HttpMethod.Patch, "/Users/" + userId, new
        {
            schemas = PatchSchemas,
            Operations = new[] { new { op = "replace", path = "active", value = false } },
        });
        staleUserPatch.Headers.IfMatch.Add(new EntityTagHeaderValue("\"stale-version\""));
        var stalePatchResponse = await scim.SendAsync(staleUserPatch);
        Assert.Equal(HttpStatusCode.PreconditionFailed, stalePatchResponse.StatusCode);

        var userPatch = await SendAsync(scim, HttpMethod.Patch, "/Users/" + userId, new
        {
            schemas = PatchSchemas,
            Operations = new[] { new { op = "replace", path = "active", value = false } },
        }, initialUserEtag);
        Assert.Equal(HttpStatusCode.OK, userPatch.StatusCode);
        Assert.NotEqual(initialUserEtag, userPatch.Headers.ETag!.Tag!.Trim('"'));
        Assert.False((await userPatch.Content.ReadFromJsonAsync<JsonElement>()).GetProperty("active").GetBoolean());

        var groupResponse = await SendAsync(scim, HttpMethod.Post, "/Groups", new
        {
            schemas = GroupSchemas,
            externalId = Guid.NewGuid().ToString("D"),
            displayName = "SCIM Protocol Group",
            active = true,
            members = new[] { new { value = userId, type = "User" } },
        });
        Assert.Equal(HttpStatusCode.Created, groupResponse.StatusCode);
        var group = await groupResponse.Content.ReadFromJsonAsync<JsonElement>();
        var groupId = group.GetProperty("id").GetString()!;
        var groupEtag = groupResponse.Headers.ETag!.Tag!.Trim('"');
        Assert.Equal(userId, group.GetProperty("members")[0].GetProperty("value").GetString());

        var groupPatch = await SendAsync(scim, HttpMethod.Patch, "/Groups/" + groupId, new
        {
            schemas = PatchSchemas,
            Operations = new[] { new { op = "replace", path = "active", value = false } },
        }, groupEtag);
        Assert.Equal(HttpStatusCode.OK, groupPatch.StatusCode);
        Assert.False((await groupPatch.Content.ReadFromJsonAsync<JsonElement>()).GetProperty("active").GetBoolean());

        var deleted = await SendAsync(scim, HttpMethod.Delete, "/Users/" + userId, null,
            userPatch.Headers.ETag!.Tag!.Trim('"'));
        Assert.Equal(HttpStatusCode.NoContent, deleted.StatusCode);
        var afterDelete = await scim.GetFromJsonAsync<JsonElement>(ScimProtocolService.BasePath + "/Users/" + userId);
        Assert.False(afterDelete.GetProperty("active").GetBoolean());

        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var actions = await db.AuthorizationAuditEvents.AsNoTracking()
            .Where(item => item.Action.StartsWith("scim."))
            .Select(item => item.Action)
            .ToListAsync();
        Assert.Contains("scim.user.created", actions);
        Assert.Contains("scim.user.patched", actions);
        Assert.Contains("scim.user.deleted", actions);
        Assert.Contains("scim.group.created", actions);
        Assert.Contains("scim.group.patched", actions);
        Assert.All(actions, action => Assert.StartsWith("scim.", action, StringComparison.Ordinal));
    }

    private HttpClient CreateScimClient(string token = IdentityApiFactory.StaticScimToken)
    {
        var client = factory.CreateClient();
        client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", token);
        return client;
    }

    private static async Task<HttpResponseMessage> SendAsync(
        HttpClient client, HttpMethod method, string path, object? body, string? etag = null)
    {
        using var request = CreateScimRequest(method, path, body);
        if (etag is not null) request.Headers.IfMatch.Add(new EntityTagHeaderValue('"' + etag + '"'));
        return await client.SendAsync(request);
    }

    private static HttpRequestMessage CreateScimRequest(HttpMethod method, string path, object? body)
    {
        var request = new HttpRequestMessage(method, ScimProtocolService.BasePath + path);
        if (body is not null)
            request.Content = new StringContent(JsonSerializer.Serialize(body), Encoding.UTF8, ScimProtocolService.ScimMediaType);
        return request;
    }
}