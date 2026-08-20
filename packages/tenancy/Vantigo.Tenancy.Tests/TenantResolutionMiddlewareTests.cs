using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Routing;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy.Tests;

public sealed class TenantResolutionMiddlewareTests
{
    [Fact]
    public async Task Skip_metadata_bypasses_tenant_resolution_without_touching_the_directory()
    {
        var nextCalled = false;
        var middleware = new TenantResolutionMiddleware(_ =>
        {
            nextCalled = true;
            return Task.CompletedTask;
        });

        var context = new DefaultHttpContext();
        context.SetEndpoint(new Endpoint(
            _ => Task.CompletedTask,
            new EndpointMetadataCollection(new SkipTenantResolutionAttribute()),
            "health"));

        await middleware.InvokeAsync(
            context,
            Options.Create(new TenancyOptions { Mode = TenancyOptions.SingleMode }),
            new ThrowingTenantDirectory());

        Assert.True(nextCalled);
    }

    [Fact]
    public async Task Missing_skip_metadata_resolves_the_default_tenant_in_single_mode()
    {
        var nextCalled = false;
        var middleware = new TenantResolutionMiddleware(_ =>
        {
            nextCalled = true;
            return Task.CompletedTask;
        });

        var defaultTenant = new TenantId(Guid.NewGuid());
        var context = new DefaultHttpContext();

        await middleware.InvokeAsync(
            context,
            Options.Create(new TenancyOptions { Mode = TenancyOptions.SingleMode }),
            new StubTenantDirectory(defaultTenant));

        Assert.True(nextCalled);
    }

    private sealed class ThrowingTenantDirectory : ITenantDirectory
    {
        public Task<TenantId?> FindActiveBySlugAsync(string slug, CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException("Tenant resolution must be skipped for this endpoint.");

        public Task<bool> IsMemberAsync(Guid userId, TenantId tenantId, CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException("Tenant resolution must be skipped for this endpoint.");

        public Task<TenantId> GetDefaultTenantAsync(CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException("Tenant resolution must be skipped for this endpoint.");

        public Task<IReadOnlyList<TenantId>> GetActiveTenantsAsync(CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException("Tenant resolution must be skipped for this endpoint.");

        public Task<IReadOnlySet<string>> GetEnabledModulesAsync(TenantId tenantId, CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException("Tenant resolution must be skipped for this endpoint.");
    }

    private sealed class StubTenantDirectory(TenantId defaultTenant) : ITenantDirectory
    {
        public Task<TenantId?> FindActiveBySlugAsync(string slug, CancellationToken cancellationToken = default) =>
            Task.FromResult<TenantId?>(defaultTenant);

        public Task<bool> IsMemberAsync(Guid userId, TenantId tenantId, CancellationToken cancellationToken = default) =>
            Task.FromResult(true);

        public Task<TenantId> GetDefaultTenantAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(defaultTenant);

        public Task<IReadOnlyList<TenantId>> GetActiveTenantsAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult<IReadOnlyList<TenantId>>([defaultTenant]);

        public Task<IReadOnlySet<string>> GetEnabledModulesAsync(TenantId tenantId, CancellationToken cancellationToken = default) =>
            Task.FromResult<IReadOnlySet<string>>(new HashSet<string>());
    }
}