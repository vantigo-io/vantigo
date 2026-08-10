using Vantigo.Energy.Domain.MeteringPoints;

namespace Vantigo.Energy.Module.Tests.Domain;

public sealed class MarketTimeZoneTests
{
    [Theory]
    [InlineData("NO1", "Europe/Oslo")]
    [InlineData("SE4", "Europe/Stockholm")]
    [InlineData("DK2", "Europe/Copenhagen")]
    [InlineData("FI1", "Europe/Helsinki")]
    [InlineData("XX1", "Europe/Oslo")]
    [InlineData(null, "Europe/Oslo")]
    public void Maps_price_area_to_market_time_zone(string? priceArea, string expected) =>
        Assert.Equal(expected, MarketTimeZone.GetId(priceArea));
}
