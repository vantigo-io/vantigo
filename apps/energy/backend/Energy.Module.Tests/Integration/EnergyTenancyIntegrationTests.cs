using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.Consumption;
using Vantigo.Energy.Domain.MeteringPoints;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Energy.Module.Tests.Integration;

[Collection(EnergyModuleCollection.Name)]
public sealed class EnergyTenancyIntegrationTests(EnergyApiFactory factory)
{
    [Fact]
    public async Task Tenant_owned_metering_points_and_readings_are_isolated_and_gsrn_is_per_tenant()
    {
        var tenantA = Guid.NewGuid();
        var tenantB = Guid.NewGuid();
        const string gsrn = "707057500000000099";
        var future = new DateTimeOffset(DateTime.UtcNow.Date.AddMonths(1), TimeSpan.Zero);

        using (factory.EnterTenant(tenantA))
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<EnergyDbContext>();
            Assert.Equal(tenantA, scope.ServiceProvider.GetRequiredService<ITenantContext>().Current.Value);
            Assert.NotEmpty(db.Model.FindEntityType(typeof(MeteringPoint))!.GetDeclaredQueryFilters());
            var point = new MeteringPoint
            {
                Gsrn = new Gsrn(gsrn),
                Address = new Address("Tenant A street", "0001", "Oslo"),
                PriceArea = "NO1",
            };
            db.MeteringPoints.Add(point);
            await db.SaveChangesAsync();

            db.ConsumptionIntervals.Add(new ConsumptionInterval
            {
                MeteringPointId = point.Id,
                Start = future,
                End = future.AddHours(1),
                QuantityKwh = 1.5m,
                Quality = ConsumptionQuality.Measured,
                Source = ConsumptionSource.Manual,
                ReceivedAt = DateTimeOffset.UtcNow,
            });
            await db.SaveChangesAsync();
            await using var transaction = await db.Database.BeginTransactionAsync();
            Assert.Single(await db.MeteringPoints.ToListAsync());
            Assert.Single(await db.ConsumptionIntervals.ToListAsync());
        }

        using (factory.EnterTenant(tenantB))
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<EnergyDbContext>();
            Assert.Equal(tenantB, scope.ServiceProvider.GetRequiredService<ITenantContext>().Current.Value);
            db.MeteringPoints.Add(new MeteringPoint
            {
                Gsrn = new Gsrn(gsrn),
                Address = new Address("Tenant B street", "0002", "Bergen"),
                PriceArea = "NO5",
            });
            await db.SaveChangesAsync();

            await using var transaction = await db.Database.BeginTransactionAsync();
            Assert.Single(await db.MeteringPoints.ToListAsync());
            var visibleReadings = await db.ConsumptionIntervals.Select(interval => interval.TenantId).ToListAsync();
            Assert.DoesNotContain(tenantA, visibleReadings);
        }
    }
}
