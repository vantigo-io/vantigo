using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.Consumption;
using Vantigo.Energy.Endpoints.Consumption.Dtos;

namespace Vantigo.Energy.Endpoints.Consumption;

internal static class AddManualConsumptionEndpoint
{
    internal static async Task<Results<Ok<ConsumptionResponse>, NotFound, ValidationProblem>> Handler(
        int id, ManualConsumptionRequest request, EnergyDbContext db, CancellationToken cancellationToken)
    {
        var validation = ConsumptionInterval.Validate(request.Start, request.End, request.QuantityKwh);
        if (validation is not null) return TypedResults.ValidationProblem(new Dictionary<string, string[]> { ["interval"] = [validation] }, title: "Invalid consumption interval");
        if (!await db.MeteringPoints.AnyAsync(point => point.Id == id, cancellationToken)) return TypedResults.NotFound();
        var previous = await db.ConsumptionIntervals.SingleOrDefaultAsync(interval => interval.MeteringPointId == id &&
            interval.Start == request.Start && interval.End == request.End && interval.IsCurrent, cancellationToken);
        if (previous is not null) previous.IsCurrent = false;
        var interval = new ConsumptionInterval
        {
            MeteringPointId = id,
            Start = request.Start,
            End = request.End,
            QuantityKwh = request.QuantityKwh,
            Quality = ConsumptionQuality.Manual,
            Source = ConsumptionSource.Manual,
            ReceivedAt = DateTimeOffset.UtcNow,
            IsCurrent = true,
            SupersedesId = previous?.Id,
            SupersedesStart = previous?.Start,
        };
        db.ConsumptionIntervals.Add(interval);
        await db.Database.ExecuteSqlInterpolatedAsync(
            $"SELECT energy.ensure_consumption_partition({request.Start.UtcDateTime.Date:yyyy-MM-dd}::date)", cancellationToken);
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.Ok(ConsumptionResponse.FromDomain(interval));
    }
}