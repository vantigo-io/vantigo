using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints.Consumption.Dtos;

namespace Vantigo.Energy.Endpoints.Customers;

internal static class GetCustomerConsumptionEndpoint
{
    internal static async Task<Results<Ok<IReadOnlyList<ConsumptionResponse>>, ProblemHttpResult>> Handler(
        int customerId, int? meteringPointId, DateTimeOffset? from, DateTimeOffset? to,
        EnergyDbContext db, CancellationToken cancellationToken)
    {
        if (from is not null && to is not null && to <= from)
            return TypedResults.Problem(title: "Invalid interval", detail: "to must be later than from.", statusCode: 400);

        var intervals = CustomerConsumptionFilter.CurrentIntervals(db, customerId, meteringPointId);
        if (from is not null) intervals = intervals.Where(interval => interval.Start >= from);
        if (to is not null) intervals = intervals.Where(interval => interval.End <= to);
        var result = await intervals.OrderBy(interval => interval.Start).Select(interval => new ConsumptionResponse(
            interval.Id, interval.MeteringPointId, interval.Start, interval.End, interval.QuantityKwh,
            interval.Quality.ToString(), interval.Source.ToString(), interval.ReceivedAt)).ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<ConsumptionResponse>>(result);
    }
}