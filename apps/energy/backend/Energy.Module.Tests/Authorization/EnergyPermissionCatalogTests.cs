using Vantigo.Contracts.Authorization;
using Vantigo.Energy.Authorization;

namespace Vantigo.Energy.Module.Tests.Authorization;

public sealed class EnergyPermissionCatalogTests
{
    [Fact]
    public void EnergyContributorRegistersStablePermissionMetadata()
    {
        var catalog = PermissionCatalog.Create([new EnergyPermissionCatalogContributor()]);

        Assert.Equal(
            [
                "energy:consumption-manage",
                "energy:consumption-view",
                "energy:metering-points-manage",
                "energy:metering-points-view",
                "energy:meters-manage",
                "energy:meters-view",
                "energy:supply-periods-manage",
                "energy:supply-periods-view",
            ],
            catalog.Permissions.Select(permission => permission.Key).ToArray());
        Assert.All(catalog.Permissions, permission =>
        {
            Assert.Equal("energy", permission.Module);
            Assert.Equal("Energy", permission.Category);
            Assert.False(string.IsNullOrWhiteSpace(permission.DisplayName));
            Assert.False(string.IsNullOrWhiteSpace(permission.Description));
        });
    }
}
