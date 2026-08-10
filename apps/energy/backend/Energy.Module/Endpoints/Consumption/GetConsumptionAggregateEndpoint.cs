using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.MeteringPoints;
using Vantigo.Energy.Endpoints.Consumption.Dtos;

namespace Vantigo.Energy.Endpoints.Consumption;

internal static class GetConsumptionAggregateEndpoint
{
    internal static async Task<Results<Ok<IReadOnlyList<ConsumptionAggregateResponse>>, NotFound, ValidationProblem>> Handler(
        int id,
        DateTimeOffset? from,
        DateTimeOffset? to,
        string? resolution,
        EnergyDbContext db,
        CancellationToken cancellationToken)
    {
        if (!ConsumptionAggregateValidation.TryValidate(from, to, resolution, out var normalizedResolution, out var errors))
            return TypedResults.ValidationProblem(errors);

        var point = await db.MeteringPoints.AsNoTracking()
            .Where(item => item.Id == id)
            .Select(item => new { item.Id, item.PriceArea })
            .SingleOrDefaultAsync(cancellationToken);
        if (point is null) return TypedResults.NotFound();

        var result = await ConsumptionAggregateQuery.ForMeteringPointAsync(
            db, point.Id, from!.Value, to!.Value, normalizedResolution,
            MarketTimeZone.GetId(point.PriceArea), cancellationToken);
        return TypedResults.Ok(result);
    }
}