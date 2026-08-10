using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.MeteringPoints;
using Vantigo.Energy.Endpoints.Consumption;
using Vantigo.Energy.Endpoints.Consumption.Dtos;

namespace Vantigo.Energy.Endpoints.Customers;

internal static class GetCustomerConsumptionAggregateEndpoint
{
    internal static async Task<Results<Ok<IReadOnlyList<CustomerConsumptionAggregateResponse>>, ValidationProblem>> Handler(
        int customerId,
        int? meteringPointId,
        DateTimeOffset? from,
        DateTimeOffset? to,
        string? resolution,
        EnergyDbContext db,
        CancellationToken cancellationToken)
    {
        if (!ConsumptionAggregateValidation.TryValidate(from, to, resolution, out var normalizedResolution, out var errors))
            return TypedResults.ValidationProblem(errors);

        var periods = CustomerConsumptionFilter.ActivePeriods(db, customerId, meteringPointId);
        var points = await db.MeteringPoints.AsNoTracking()
            .Where(point => periods.Any(period => period.MeteringPointId == point.Id))
            .Select(point => new { point.Id, point.PriceArea })
            .ToListAsync(cancellationToken);

        var result = new List<CustomerConsumptionAggregateResponse>();
        foreach (var point in points)
        {
            var aggregates = await ConsumptionAggregateQuery.ForCustomerMeteringPointAsync(
                db, customerId, point.Id, from!.Value, to!.Value, normalizedResolution,
                MarketTimeZone.GetId(point.PriceArea), cancellationToken);
            result.AddRange(aggregates);
        }

        return TypedResults.Ok<IReadOnlyList<CustomerConsumptionAggregateResponse>>(result
            .OrderBy(item => item.MeteringPointId)
            .ThenBy(item => item.BucketStart)
            .ToList());
    }
}