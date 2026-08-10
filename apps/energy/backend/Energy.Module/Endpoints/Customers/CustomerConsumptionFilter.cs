using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.Consumption;
using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Endpoints.Customers;

internal static class CustomerConsumptionFilter
{
    internal static IQueryable<SupplyPeriod> ActivePeriods(
        EnergyDbContext db, int customerId, int? meteringPointId) =>
        db.SupplyPeriods.AsNoTracking()
            .Where(period => period.CustomerId == customerId && period.Status != SupplyPeriodStatus.Cancelled &&
                (meteringPointId == null || period.MeteringPointId == meteringPointId));

    internal static IQueryable<ConsumptionInterval> CurrentIntervals(
        EnergyDbContext db, int customerId, int? meteringPointId)
    {
        var periods = ActivePeriods(db, customerId, meteringPointId);
        return db.ConsumptionIntervals.AsNoTracking().Where(interval => interval.IsCurrent &&
            periods.Any(period => period.MeteringPointId == interval.MeteringPointId &&
                interval.Start >= period.Start && (period.End == null || interval.End <= period.End)));
    }
}