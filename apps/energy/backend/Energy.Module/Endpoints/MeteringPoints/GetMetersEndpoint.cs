using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

namespace Vantigo.Energy.Endpoints.MeteringPoints;

internal static class GetMetersEndpoint
{
    internal static async Task<Results<Ok<IReadOnlyList<MeterResponse>>, NotFound>> Handler(
        int id, EnergyDbContext db, CancellationToken cancellationToken)
    {
        if (!await db.MeteringPoints.AnyAsync(point => point.Id == id, cancellationToken)) return TypedResults.NotFound();
        var meters = await db.Meters.AsNoTracking().Where(meter => meter.MeteringPointId == id)
            .OrderBy(meter => meter.InstalledAt).Select(meter => new MeterResponse(
                meter.Id, meter.MeteringPointId, meter.MeterNumber, meter.InstalledAt, meter.RemovedAt)).ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<MeterResponse>>(meters);
    }
}