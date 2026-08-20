using Vantigo.Energy.Domain.Consumption;

namespace Vantigo.Energy.Module.Tests.Domain;

public sealed class ConsumptionIntervalTests
{
    [Fact]
    public void Rejects_end_before_start() => Assert.Equal("End must be later than start.", ConsumptionInterval.Validate(DateTimeOffset.UtcNow, DateTimeOffset.UtcNow.AddHours(-1), 1));

    [Fact]
    public void Rejects_negative_quantity() => Assert.Equal("Quantity must be zero or greater.", ConsumptionInterval.Validate(DateTimeOffset.UtcNow, DateTimeOffset.UtcNow.AddHours(1), -1));
}