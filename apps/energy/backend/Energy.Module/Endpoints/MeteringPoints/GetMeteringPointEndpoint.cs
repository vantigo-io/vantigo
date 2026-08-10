using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

namespace Vantigo.Energy.Endpoints.MeteringPoints;

internal static class GetMeteringPointEndpoint
{
    internal static async Task<Results<Ok<MeteringPointResponse>, NotFound>> Handler(int id, EnergyDbContext db, CancellationToken cancellationToken)
    {
        var point = await db.MeteringPoints.AsNoTracking().Include(item => item.Meters).SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        return point is null ? TypedResults.NotFound() : TypedResults.Ok(MeteringPointResponse.FromDomain(point));
    }
}