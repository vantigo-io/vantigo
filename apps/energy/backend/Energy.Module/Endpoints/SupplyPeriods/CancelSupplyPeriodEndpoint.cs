using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Endpoints.SupplyPeriods;

internal static class CancelSupplyPeriodEndpoint
{
    internal static async Task<Results<NoContent, NotFound>> Handler(int id, int periodId, EnergyDbContext db, CancellationToken cancellationToken)
    {
        var period = await db.SupplyPeriods.SingleOrDefaultAsync(item => item.Id == periodId && item.MeteringPointId == id, cancellationToken);
        if (period is null) return TypedResults.NotFound();
        period.Status = SupplyPeriodStatus.Cancelled;
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.NoContent();
    }
}