using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Contracts.Authorization;
using Vantigo.Identity.Authorization;

namespace Vantigo.Identity.Tests.Authorization;

public sealed class AuthorizationCatalogTests
{
    private static PermissionDescriptor Permission(string key, bool delegable = true) =>
        new(key, key, $"Description for {key}", key[..key.IndexOf(':')], "Tests", Delegable: delegable);

    [Fact]
    public void CatalogResolutionIsDeferredUntilAllContributorsAreRegistered()
    {
        var services = new ServiceCollection();
        services.AddPermissionCatalog(builder => builder.Add(Permission("identity:manage")));
        services.AddSingleton<IPermissionCatalogContributor, DeferredContributor>();

        using var provider = services.BuildServiceProvider();
        var catalog = provider.GetRequiredService<IPermissionCatalog>();

        Assert.Equal(["customers:view", "identity:manage"],
            catalog.Permissions.Select(permission => permission.Key).ToArray());
    }

    [Fact]
    public void ContributorsMayBeRegisteredBeforeFinalizationAndDoubleFinalizationThrows()
    {
        var services = new ServiceCollection();
        services.AddPermissionCatalogContributors([
            new DeferredContributor(),
            new AnotherContributor(),
        ]);
        services.AddPermissionCatalog(builder => builder.Add(Permission("identity:manage")));

        using var provider = services.BuildServiceProvider();
        var catalog = provider.GetRequiredService<IPermissionCatalog>();
        Assert.Equal(["customers:view", "energy:view", "identity:manage"],
            catalog.Permissions.Select(permission => permission.Key).ToArray());

        Assert.Throws<InvalidOperationException>(() => services.AddPermissionCatalog());
    }

    [Fact]
    public void DuplicateAndMalformedCatalogEntriesFailValidation()
    {
        var exception = Assert.Throws<InvalidOperationException>(() => PermissionCatalog.Create([
            Permission("identity:manage"),
            Permission("identity:manage"),
            Permission("Customers:View"),
        ]));

        Assert.Contains("registered more than once", exception.Message, StringComparison.Ordinal);
        Assert.Contains("malformed", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void EndpointPermissionMetadataMustBeRegisteredAtStartup()
    {
        var builder = WebApplication.CreateBuilder();
        builder.Services.AddRouting();
        using var app = builder.Build();
        app.MapGet("/registered", () => Results.Ok())
            .RequirePermission("identity:manage");
        app.MapGet("/missing", () => Results.Ok())
            .RequirePermission("customers:view");

        var catalog = PermissionCatalog.Create([Permission("identity:manage")]);
        var exception = Assert.Throws<InvalidOperationException>(() => app.ValidatePermissionCatalog(catalog));

        Assert.Contains("customers:view", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void ValidEndpointPermissionIsAcceptedByStartupValidation()
    {
        var builder = WebApplication.CreateBuilder();
        builder.Services.AddRouting();
        using var app = builder.Build();
        app.MapGet("/registered", () => Results.Ok())
            .RequirePermission("identity:manage");

        var catalog = PermissionCatalog.Create([Permission("identity:manage")]);
        app.ValidatePermissionCatalog(catalog);
    }

    private sealed class DeferredContributor : IPermissionCatalogContributor
    {
        public void Contribute(PermissionCatalogBuilder catalog) => catalog.Add(Permission("customers:view"));
    }

    private sealed class AnotherContributor : IPermissionCatalogContributor
    {
        public void Contribute(PermissionCatalogBuilder catalog) => catalog.Add(Permission("energy:view"));
    }
}