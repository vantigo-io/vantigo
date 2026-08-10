using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints.Consumption.Dtos;

namespace Vantigo.Energy.Endpoints.Consumption;

internal static class GetConsumptionEndpoint
{
    internal static async Task<Results<Ok<IReadOnlyList<ConsumptionResponse>>, NotFound, ProblemHttpResult>> Handler(
        int id, DateTimeOffset? from, DateTimeOffset? to, EnergyDbContext db, CancellationToken cancellationToken)
    {
        if (from is not null && to is not null && to <= from)
            return TypedResults.Problem(title: "Invalid interval", detail: "to must be later than from.", statusCode: 400);
        if (!await db.MeteringPoints.AnyAsync(point => point.Id == id, cancellationToken)) return TypedResults.NotFound();
        var query = db.ConsumptionIntervals.AsNoTracking().Where(interval => interval.MeteringPointId == id && interval.IsCurrent);
        if (from is not null) query = query.Where(interval => interval.Start >= from);
        if (to is not null) query = query.Where(interval => interval.End <= to);
        var rows = await query.OrderBy(interval => interval.Start).Select(interval => new ConsumptionResponse(
            interval.Id, interval.MeteringPointId, interval.Start, interval.End, interval.QuantityKwh,
            interval.Quality.ToString(), interval.Source.ToString(), interval.ReceivedAt)).ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<ConsumptionResponse>>(rows);
    }
}