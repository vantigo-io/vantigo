using Vantigo.Energy.Domain.Meters;

namespace Vantigo.Energy.Module.Tests.Domain;

public sealed class MeterTests
{
    [Theory]
    [InlineData(null, "Meter number is required.")]
    [InlineData("", "Meter number is required.")]
    [InlineData("  ", "Meter number is required.")]
    public void Rejects_missing_meter_number(string? value, string expected) => Assert.Equal(expected, Meter.ValidateMeterNumber(value));

    [Fact]
    public void Rejects_meter_number_over_64_characters() => Assert.Equal(
        "Meter number cannot be longer than 64 characters.", Meter.ValidateMeterNumber(new string('x', 65)));

    [Fact]
    public void Trimming_is_allowed() => Assert.Null(Meter.ValidateMeterNumber(" MTR-1 "));
}