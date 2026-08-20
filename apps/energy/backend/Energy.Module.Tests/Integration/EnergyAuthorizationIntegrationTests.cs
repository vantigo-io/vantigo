using System.Net;
using System.Net.Http.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Energy.Module.Tests.Integration;

[Collection(EnergyModuleCollection.Name)]
public sealed class EnergyAuthorizationIntegrationTests(EnergyApiFactory factory)
{
    private static readonly EndpointCase[] Endpoints =
    [
        new("GET", "/api/v1/energy/metering-points", "energy:metering-points-view", AdditionalPermissions: ["energy:meters-view"]),
        new("POST", "/api/v1/energy/metering-points", "energy:metering-points-manage", NewMeteringPointBody,
            ["energy:metering-points-view", "energy:meters-manage", "energy:meters-view"]),
        new("GET", "/api/v1/energy/metering-points/999999", "energy:metering-points-view", AdditionalPermissions: ["energy:meters-view"]),
        new("PUT", "/api/v1/energy/metering-points/999999", "energy:metering-points-manage", NewMeteringPointBody,
            ["energy:metering-points-view", "energy:meters-view"]),
        new("GET", "/api/v1/energy/metering-points/999999/meters", "energy:meters-view"),
        new("POST", "/api/v1/energy/metering-points/999999/meters", "energy:meters-manage", ReplaceMeterBody,
            ["energy:meters-view"]),
        new("GET", "/api/v1/energy/metering-points/999999/consumption", "energy:consumption-view"),
        new("GET", "/api/v1/energy/metering-points/999999/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&resolution=day", "energy:consumption-view"),
        new("POST", "/api/v1/energy/metering-points/999999/consumption", "energy:consumption-manage", ManualConsumptionBody,
            ["energy:consumption-view"]),
        new("GET", "/api/v1/energy/metering-points/999999/supply-periods", "energy:supply-periods-view"),
        new("POST", "/api/v1/energy/metering-points/999999/supply-periods", "energy:supply-periods-manage", CreateSupplyPeriodBody,
            ["energy:supply-periods-view"]),
        new("POST", "/api/v1/energy/metering-points/999999/supply-periods/switch", "energy:supply-periods-manage", SwitchSupplyPeriodBody,
            ["energy:supply-periods-view"]),
        new("POST", "/api/v1/energy/metering-points/999999/supply-periods/999999/end", "energy:supply-periods-manage", EndSupplyPeriodBody,
            ["energy:supply-periods-view"]),
        new("DELETE", "/api/v1/energy/metering-points/999999/supply-periods/999999", "energy:supply-periods-manage"),
        new("GET", "/api/v1/energy/customers/1001/metering-points", "energy:metering-points-view",
            AdditionalPermissions: ["energy:meters-view", "energy:supply-periods-view"]),
        new("GET", "/api/v1/energy/customers/1001/consumption", "energy:consumption-view"),
        new("GET", "/api/v1/energy/customers/1001/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&resolution=day", "energy:consumption-view"),
    ];

    [Fact]
    public async Task EveryEnergyEndpointRequiresItsRegisteredEnergyPermission()
    {
        var viewPermissions = new[]
        {
            "energy:metering-points-view",
            "energy:meters-view",
            "energy:consumption-view",
            "energy:supply-periods-view",
        };
        var managePermissions = new[]
        {
            "energy:metering-points-manage",
            "energy:meters-manage",
            "energy:consumption-manage",
            "energy:supply-periods-manage",
        };
        var noPermission = await CreateUserAsync([]);
        var viewOnly = await CreateUserAsync(viewPermissions);
        var manageOnly = await CreateUserAsync(managePermissions);
        var allPermissions = await CreateUserAsync(Endpoints.SelectMany(endpoint => endpoint.RequiredPermissions).Distinct().ToArray());
        var disabled = await CreateUserAsync(Endpoints.SelectMany(endpoint => endpoint.RequiredPermissions).Distinct().ToArray());

        using var owner = factory.CreateAuthenticatedClient();
        using var noPermissionClient = await factory.CreateAuthenticatedClientAsync(noPermission.Email, noPermission.Password);
        using var viewOnlyClient = await factory.CreateAuthenticatedClientAsync(viewOnly.Email, viewOnly.Password);
        using var manageOnlyClient = await factory.CreateAuthenticatedClientAsync(manageOnly.Email, manageOnly.Password);
        using var allPermissionsClient = await factory.CreateAuthenticatedClientAsync(allPermissions.Email, allPermissions.Password);
        using var disabledClient = await factory.CreateAuthenticatedClientAsync(disabled.Email, disabled.Password);
        await DisableUserAsync(disabled.Id);

        foreach (var endpoint in Endpoints)
        {
            Assert.NotEqual(HttpStatusCode.Forbidden, (await SendAsync(owner, endpoint)).StatusCode);
            Assert.Equal(HttpStatusCode.Forbidden, (await SendAsync(noPermissionClient, endpoint)).StatusCode);

            var viewResponse = await SendAsync(viewOnlyClient, endpoint);
            if (endpoint.RequiredPermissions.All(viewPermissions.Contains))
                Assert.NotEqual(HttpStatusCode.Forbidden, viewResponse.StatusCode);
            else
                Assert.Equal(HttpStatusCode.Forbidden, viewResponse.StatusCode);

            var manageResponse = await SendAsync(manageOnlyClient, endpoint);
            if (endpoint.RequiredPermissions.All(managePermissions.Contains))
                Assert.NotEqual(HttpStatusCode.Forbidden, manageResponse.StatusCode);
            else
                Assert.Equal(HttpStatusCode.Forbidden, manageResponse.StatusCode);

            Assert.NotEqual(HttpStatusCode.Forbidden, (await SendAsync(allPermissionsClient, endpoint)).StatusCode);

            var disabledResponse = await SendAsync(disabledClient, endpoint);
            Assert.True(disabledResponse.StatusCode is HttpStatusCode.Unauthorized or HttpStatusCode.Forbidden,
                $"Disabled user unexpectedly reached {endpoint.Method} {endpoint.Path}: {disabledResponse.StatusCode}");
        }
    }

    [Fact]
    public async Task Customer_metering_points_requires_all_permissions_for_returned_data()
    {
        var meteringPointsOnly = await CreateUserAsync(["energy:metering-points-view"]);
        var metersOnly = await CreateUserAsync(["energy:meters-view"]);
        var supplyPeriodsOnly = await CreateUserAsync(["energy:supply-periods-view"]);
        var completeView = await CreateUserAsync([
            "energy:metering-points-view",
            "energy:meters-view",
            "energy:supply-periods-view",
        ]);

        using var ownerClient = factory.CreateAuthenticatedClient();
        using var meteringPointsClient = await factory.CreateAuthenticatedClientAsync(meteringPointsOnly.Email, meteringPointsOnly.Password);
        using var metersClient = await factory.CreateAuthenticatedClientAsync(metersOnly.Email, metersOnly.Password);
        using var supplyPeriodsClient = await factory.CreateAuthenticatedClientAsync(supplyPeriodsOnly.Email, supplyPeriodsOnly.Password);
        using var completeViewClient = await factory.CreateAuthenticatedClientAsync(completeView.Email, completeView.Password);

        const string path = "/api/v1/energy/customers/1001/metering-points";
        Assert.NotEqual(HttpStatusCode.Forbidden, (await ownerClient.GetAsync(path)).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, (await meteringPointsClient.GetAsync(path)).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, (await metersClient.GetAsync(path)).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, (await supplyPeriodsClient.GetAsync(path)).StatusCode);
        Assert.NotEqual(HttpStatusCode.Forbidden, (await completeViewClient.GetAsync(path)).StatusCode);
    }

    [Fact]
    public async Task Composite_resource_endpoints_require_each_related_permission()
    {
        var requiredPermissions = Endpoints
            .Where(endpoint => endpoint.HasAdditionalPermissions)
            .SelectMany(endpoint => endpoint.RequiredPermissions)
            .Distinct(StringComparer.Ordinal)
            .ToArray();
        var complete = await CreateUserAsync(requiredPermissions);
        using var ownerClient = factory.CreateAuthenticatedClient();
        using var completeClient = await factory.CreateAuthenticatedClientAsync(complete.Email, complete.Password);

        foreach (var endpoint in Endpoints.Where(endpoint => endpoint.HasAdditionalPermissions))
        {
            Assert.NotEqual(HttpStatusCode.Forbidden, (await SendAsync(ownerClient, endpoint)).StatusCode);
            Assert.NotEqual(HttpStatusCode.Forbidden, (await SendAsync(completeClient, endpoint)).StatusCode);

            foreach (var missingPermission in endpoint.RequiredPermissions)
            {
                var narrow = await CreateUserAsync(requiredPermissions.Where(permission => permission != missingPermission).ToArray());
                using var narrowClient = await factory.CreateAuthenticatedClientAsync(narrow.Email, narrow.Password);
                var response = await SendAsync(narrowClient, endpoint);
                Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
            }
        }
    }

    private async Task<UserFixture> CreateUserAsync(IReadOnlyCollection<string> permissions)
    {
        var email = $"energy-rbac-{Guid.NewGuid():N}@integration.test";
        const string password = "IntegrationUserPassword123";
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var roles = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>();
        var roleName = $"energy-test-{Guid.NewGuid():N}";
        var user = new ApplicationUser
        {
            UserName = email,
            Email = email,
            EmailConfirmed = true,
            DisplayName = "Energy authorization test user",
        };
        var userResult = await users.CreateAsync(user, password);
        Assert.True(userResult.Succeeded, string.Join("; ", userResult.Errors.Select(error => error.Description)));
        var userRole = await users.AddToRoleAsync(user, AuthRoles.User);
        Assert.True(userRole.Succeeded, string.Join("; ", userRole.Errors.Select(error => error.Description)));
        if (permissions.Count > 0)
        {
            var role = new IdentityRole<Guid>(roleName)
            {
                NormalizedName = roleName.ToUpperInvariant(),
            };
            var roleResult = await roles.CreateAsync(role);
            Assert.True(roleResult.Succeeded, string.Join("; ", roleResult.Errors.Select(error => error.Description)));
            db.RoleMetadata.Add(new RoleMetadata
            {
                RoleId = role.Id,
                DisplayName = roleName,
                Description = "Energy authorization integration role",
                IsSystem = false,
                IsBuiltIn = false,
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            });
            db.RolePermissions.AddRange(permissions.Select(permission => new RolePermission
            {
                RoleId = role.Id,
                PermissionKey = permission,
            }));
            var assignment = await users.AddToRoleAsync(user, roleName);
            Assert.True(assignment.Succeeded, string.Join("; ", assignment.Errors.Select(error => error.Description)));
        }
        await db.SaveChangesAsync();
        return new UserFixture(user.Id, email, password);
    }

    private async Task DisableUserAsync(Guid userId)
    {
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var user = await db.Users.SingleAsync(item => item.Id == userId);
        user.IsDisabled = true;
        await db.SaveChangesAsync();
    }

    private static async Task<HttpResponseMessage> SendAsync(HttpClient client, EndpointCase endpoint)
    {
        using var request = new HttpRequestMessage(new HttpMethod(endpoint.Method), endpoint.Path);
        if (endpoint.Body is not null)
            request.Content = JsonContent.Create(endpoint.Body());
        return await client.SendAsync(request);
    }

    private static object NewMeteringPointBody() => new
    {
        gsrn = "707057500000000001",
        meterNumber = "Energy authorization test meter",
        address = new { streetAddress = "Testgata 1", postalCode = "0001", city = "Oslo", countryCode = "NO" },
        priceArea = "NO1",
        connectionStatus = "Connected",
    };

    private static object ReplaceMeterBody() => new { meterNumber = "Replacement", installedAt = DateTimeOffset.UtcNow };
    private static object ManualConsumptionBody() => new { start = DateTimeOffset.UtcNow.AddHours(-2), end = DateTimeOffset.UtcNow.AddHours(-1), quantityKwh = 1m };
    private static object CreateSupplyPeriodBody() => new { customerId = 1001, start = DateTimeOffset.UtcNow.AddDays(-1) };
    private static object SwitchSupplyPeriodBody() => new { customerId = 1001, switchAt = DateTimeOffset.UtcNow };
    private static object EndSupplyPeriodBody() => new { end = DateTimeOffset.UtcNow };

    private sealed record EndpointCase(
        string Method,
        string Path,
        string Permission,
        Func<object>? Body = null,
        IReadOnlyCollection<string>? AdditionalPermissions = null)
    {
        public bool HasAdditionalPermissions => AdditionalPermissions is { Count: > 0 };

        public IReadOnlyCollection<string> RequiredPermissions =>
            [Permission, .. AdditionalPermissions ?? []];
    }
    private sealed record UserFixture(Guid Id, string Email, string Password);
}