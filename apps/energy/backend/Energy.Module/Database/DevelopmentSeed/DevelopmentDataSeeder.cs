using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.Consumption;
using Vantigo.Energy.Domain.MeteringPoints;
using Vantigo.Energy.Domain.Meters;
using Vantigo.Energy.Domain.SupplyPeriods;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Energy.Database.DevelopmentSeed;

internal static class DevelopmentDataSeeder
{
    private const int SeedIntervalHours = 24 * 90;
    private static readonly DateTimeOffset SeedNow = new(2026, 8, 10, 0, 0, 0, TimeSpan.Zero);

    internal static async Task SeedEnergyAsync(this IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
        var tenantDirectory = scope.ServiceProvider.GetRequiredService<ITenantDirectory>();
        using var tenantScope = AmbientTenantContext.Enter(
            await tenantDirectory.GetDefaultTenantAsync(cancellationToken));
        var db = scope.ServiceProvider.GetRequiredService<EnergyDbContext>();
        var definitions = new[]
        {
            ("707057500000000001", "MTR-AURORA-001", "NO1", "Oslo", 1001),
            ("707057500000000002", "MTR-FJORD-002", "NO5", "Bergen", 1002),
            ("707057500000000003", "MTR-NORTH-003", "NO3", "Trondheim", 1003),
        };

        foreach (var (gsrn, meter, area, city, customerId) in definitions)
        {
            var point = await db.MeteringPoints.Include(item => item.SupplyPeriods).Include(item => item.Meters)
                .SingleOrDefaultAsync(item => item.Gsrn == new Gsrn(gsrn), cancellationToken);
            if (point is null)
            {
                point = new MeteringPoint
                {
                    Gsrn = new Gsrn(gsrn),
                    Address = new Address($"{city}veien 1", "0001", city),
                    PriceArea = area,
                    ConnectionStatus = ConnectionStatus.Connected,
                };
                point.Meters.Add(new Meter { MeterNumber = meter, InstalledAt = SeedNow.AddDays(-30) });
                db.MeteringPoints.Add(point);
                await db.SaveChangesAsync(cancellationToken);
            }

            if (!point.Meters.Any(meterItem => meterItem.RemovedAt is null))
            {
                point.Meters.Add(new Meter { MeterNumber = meter, InstalledAt = point.CreatedAt == default ? SeedNow : point.CreatedAt });
            }

            if (gsrn == "707057500000000002" && !point.Meters.Any(meterItem => meterItem.MeterNumber == "MTR-FJORD-OLD"))
            {
                var activeMeter = point.Meters.Single(meterItem => meterItem.RemovedAt is null);
                point.Meters.Add(new Meter
                {
                    MeterNumber = "MTR-FJORD-OLD",
                    InstalledAt = activeMeter.InstalledAt.AddDays(-365),
                    RemovedAt = activeMeter.InstalledAt,
                });
            }

            if (!point.SupplyPeriods.Any())
            {
                db.SupplyPeriods.Add(new SupplyPeriod
                {
                    MeteringPointId = point.Id,
                    CustomerId = customerId,
                    Start = SeedNow.AddDays(-30),
                    Status = SupplyPeriodStatus.Active,
                });
            }

            var existingStarts = await db.ConsumptionIntervals
                .Where(item => item.MeteringPointId == point.Id)
                .Select(item => item.Start)
                .ToHashSetAsync(cancellationToken);
            var intervals = new List<ConsumptionInterval>(SeedIntervalHours);
            for (var hour = SeedIntervalHours; hour > 0; hour--)
            {
                var start = SeedNow.AddHours(-hour);
                if (existingStarts.Contains(start)) continue;

                var localHour = start.UtcDateTime.Hour;
                var isWeekend = start.UtcDateTime.DayOfWeek is DayOfWeek.Saturday or DayOfWeek.Sunday;
                var nightFactor = localHour is >= 0 and < 6 ? 0.58m : localHour is >= 18 and < 23 ? 1.18m : 1m;
                var workdayFactor = isWeekend ? 0.78m : localHour is >= 8 and < 17 ? 1.22m : 0.9m;
                var seasonalFactor = 1m + (decimal)Math.Sin((start - SeedNow.AddDays(-365)).TotalDays / 365d * Math.PI * 2d) * 0.08m;
                var baseConsumption = 0.72m + point.Id % 3 * 0.16m;
                var variation = 1m + (decimal)((hour * 17 + point.Id * 13) % 11 - 5) / 100m;
                intervals.Add(new ConsumptionInterval
                {
                    MeteringPointId = point.Id,
                    Start = start,
                    End = start.AddHours(1),
                    QuantityKwh = Math.Round(baseConsumption * nightFactor * workdayFactor * seasonalFactor * variation, 3),
                    Quality = ConsumptionQuality.Measured,
                    Source = ConsumptionSource.Elhub,
                    ReceivedAt = start.AddHours(1),
                    IsCurrent = true,
                });
            }
            db.ConsumptionIntervals.AddRange(intervals);
        }

        await db.SaveChangesAsync(cancellationToken);
    }
}