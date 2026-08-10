using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.SupplyPeriods;
using Vantigo.Energy.Endpoints.SupplyPeriods.Dtos;

namespace Vantigo.Energy.Endpoints.SupplyPeriods;

internal static class EndSupplyPeriodEndpoint
{
    internal static async Task<Results<Ok<SupplyPeriodResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id, int periodId, EndSupplyPeriodRequest request, EnergyDbContext db, CancellationToken cancellationToken)
    {
        var period = await db.SupplyPeriods.SingleOrDefaultAsync(item => item.Id == periodId && item.MeteringPointId == id, cancellationToken);
        if (period is null) return TypedResults.NotFound();
        if (period.Status == SupplyPeriodStatus.Cancelled) return TypedResults.Problem(title: "Invalid supply period", detail: "A cancelled period cannot be ended.", statusCode: 400);
        if (SupplyPeriod.Validate(period.Start, request.End) is { } validation)
            return TypedResults.ValidationProblem(new Dictionary<string, string[]> { ["end"] = [validation] }, title: "Invalid supply period");
        period.End = request.End;
        period.Status = SupplyPeriodStatus.Ended;
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.Ok(SupplyPeriodResponse.FromDomain(period));
    }
}