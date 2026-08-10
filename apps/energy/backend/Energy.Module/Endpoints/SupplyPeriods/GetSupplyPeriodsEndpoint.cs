using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints.SupplyPeriods.Dtos;

namespace Vantigo.Energy.Endpoints.SupplyPeriods;

internal static class GetSupplyPeriodsEndpoint
{
    internal static async Task<Results<Ok<IReadOnlyList<SupplyPeriodResponse>>, NotFound>> Handler(int id, EnergyDbContext db, CancellationToken cancellationToken)
    {
        if (!await db.MeteringPoints.AnyAsync(point => point.Id == id, cancellationToken)) return TypedResults.NotFound();
        var periods = await db.SupplyPeriods.AsNoTracking().Where(period => period.MeteringPointId == id)
            .OrderBy(period => period.Start).Select(period => new SupplyPeriodResponse(
                period.Id, period.MeteringPointId, period.CustomerId, period.Start, period.End, period.Status.ToString())).ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<SupplyPeriodResponse>>(periods);
    }
}