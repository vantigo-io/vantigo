using Vantigo.Energy.Domain.MeteringPoints;

namespace Vantigo.Energy.Module.Tests.Domain;

public sealed class PriceAreaTests
{
    [Theory]
    [InlineData("NO1")]
    [InlineData("SE4")]
    [InlineData("DK2")]
    public void Accepts_supported_price_area_format(string value) => Assert.True(PriceArea.IsValid(value));

    [Theory]
    [InlineData("N1")]
    [InlineData("no1")]
    [InlineData("NO123")]
    public void Rejects_invalid_price_area_format(string value) => Assert.False(PriceArea.IsValid(value));
}
