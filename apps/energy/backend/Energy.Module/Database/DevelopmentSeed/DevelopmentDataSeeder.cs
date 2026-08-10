using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.Consumption;
using Vantigo.Energy.Domain.MeteringPoints;
using Vantigo.Energy.Domain.Meters;
using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Database.DevelopmentSeed;

internal static class DevelopmentDataSeeder
{
    private static readonly DateTimeOffset SeedNow = new(2026, 8, 10, 0, 0, 0, TimeSpan.Zero);

    internal static async Task SeedEnergyAsync(this IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
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

            if (!await db.ConsumptionIntervals.AnyAsync(item => item.MeteringPointId == point.Id, cancellationToken))
            {
                for (var hour = 72; hour > 0; hour--)
                {
                    db.ConsumptionIntervals.Add(new ConsumptionInterval
                    {
                        MeteringPointId = point.Id,
                        Start = SeedNow.AddHours(-hour),
                        End = SeedNow.AddHours(-hour + 1),
                        QuantityKwh = 0.8m + (hour % 5) * 0.1m,
                        Quality = ConsumptionQuality.Measured,
                        Source = ConsumptionSource.Elhub,
                        ReceivedAt = SeedNow.AddHours(-hour + 1),
                        IsCurrent = true,
                    });
                }
            }
        }

        await db.SaveChangesAsync(cancellationToken);
    }
}