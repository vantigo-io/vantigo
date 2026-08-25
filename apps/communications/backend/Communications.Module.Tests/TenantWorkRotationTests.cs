using Vantigo.Communications.Services;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Module.Tests;

public sealed class TenantWorkRotationTests
{
    [Fact]
    public void Every_tenant_is_periodically_first_in_line()
    {
        var tenants = new[] { TenantId.New(), TenantId.New(), TenantId.New() };

        var firstScan = TenantWorkRotation.Rotate(tenants, 0).ToArray();
        var secondScan = TenantWorkRotation.Rotate(tenants, 1).ToArray();
        var thirdScan = TenantWorkRotation.Rotate(tenants, 2).ToArray();

        Assert.Equal(tenants[0], firstScan[0]);
        Assert.Equal(tenants[1], secondScan[0]);
        Assert.Equal(tenants[2], thirdScan[0]);
    }

    [Fact]
    public void Each_scan_still_visits_every_tenant_exactly_once()
    {
        var tenants = new[] { TenantId.New(), TenantId.New(), TenantId.New(), TenantId.New() };

        for (var rotation = 0; rotation < 8; rotation++)
        {
            var scan = TenantWorkRotation.Rotate(tenants, rotation).ToArray();
            Assert.Equal(tenants.Length, scan.Length);
            Assert.Equal(tenants.ToHashSet(), scan.ToHashSet());
        }
    }

    [Fact]
    public void Negative_rotation_values_from_counter_wraparound_are_safe()
    {
        var tenants = new[] { TenantId.New(), TenantId.New() };

        var scan = TenantWorkRotation.Rotate(tenants, int.MinValue).ToArray();

        Assert.Equal(tenants.Length, scan.Length);
    }

    [Fact]
    public void An_empty_tenant_list_yields_nothing()
    {
        Assert.Empty(TenantWorkRotation.Rotate([], 3));
    }
}