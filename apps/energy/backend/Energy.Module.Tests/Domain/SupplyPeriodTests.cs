using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Module.Tests.Domain;

public sealed class SupplyPeriodTests
{
    [Fact]
    public void Adjacent_periods_do_not_overlap()
    {
        var start = DateTimeOffset.UtcNow;
        Assert.False(SupplyPeriod.Overlaps(start, start.AddDays(1), start.AddDays(1), null));
    }

    [Fact]
    public void Open_ended_period_overlaps_later_period()
    {
        var start = DateTimeOffset.UtcNow;
        Assert.True(SupplyPeriod.Overlaps(start, null, start.AddDays(1), null));
    }
}