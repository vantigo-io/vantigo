using Vantigo.Tenancy;

namespace Vantigo.Tenancy.Tests;

public sealed class TenantRouteTemplatesTests
{
    [Theory]
    [InlineData(
        "/api/v{version:apiVersion}/customers",
        "/api/v{version:apiVersion}/t/{tenantSlug}/customers")]
    [InlineData(
        "/api/v{version:customVersion}/energy/metering-points",
        "/api/v{version:customVersion}/t/{tenantSlug}/energy/metering-points")]
    [InlineData("/api/v1/products", "/api/v1/t/{tenantSlug}/products")]
    public void AddTenantSegment_places_tenant_after_version(
        string prefix,
        string expected)
    {
        Assert.Equal(expected, TenantRouteTemplates.AddTenantSegment(prefix));
    }

    [Fact]
    public void AddTenantSegment_rejects_non_api_version_prefix()
    {
        Assert.Throws<ArgumentException>(() => TenantRouteTemplates.AddTenantSegment("/api/products"));
    }

    [Theory]
    [InlineData("/api/v{version:apiVersion}/customers", "customers")]
    [InlineData("/api/v{version:apiVersion}/communications", "communications")]
    [InlineData("/api/v{version:apiVersion}/products", "products")]
    [InlineData("/api/v{version:apiVersion}/energy", "energy")]
    public void TryGetModuleKey_returns_canonical_module_key(string prefix, string expected)
    {
        Assert.True(TenantRouteTemplates.TryGetModuleKey(prefix, out var moduleKey));
        Assert.Equal(expected, moduleKey);
    }

    [Fact]
    public void TryGetModuleKey_rejects_unknown_module()
    {
        Assert.False(TenantRouteTemplates.TryGetModuleKey("/api/v{version:apiVersion}/identity", out _));
    }
}