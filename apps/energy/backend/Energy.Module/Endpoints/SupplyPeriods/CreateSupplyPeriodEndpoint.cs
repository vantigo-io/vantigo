using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts;
using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.SupplyPeriods;
using Vantigo.Energy.Endpoints.SupplyPeriods.Dtos;

namespace Vantigo.Energy.Endpoints.SupplyPeriods;

internal static class CreateSupplyPeriodEndpoint
{
    internal static async Task<Results<Created<SupplyPeriodResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id, CreateSupplyPeriodRequest request, EnergyDbContext db, ICustomerDirectory customerDirectory, CancellationToken cancellationToken)
    {
        if (request.CustomerId <= 0) return TypedResults.ValidationProblem(new Dictionary<string, string[]> { ["customerId"] = ["Customer ID must be greater than zero."] }, title: "Invalid supply period");
        if (SupplyPeriod.Validate(request.Start, null) is { } validation)
            return TypedResults.ValidationProblem(new Dictionary<string, string[]> { ["start"] = [validation] }, title: "Invalid supply period");
        if (!await db.MeteringPoints.AnyAsync(point => point.Id == id, cancellationToken)) return TypedResults.NotFound();
        if (await customerDirectory.FindCustomerAsync(request.CustomerId, cancellationToken) is null)
            return TypedResults.ValidationProblem(new Dictionary<string, string[]> { ["customerId"] = [$"Customer {request.CustomerId} does not exist."] }, title: "Invalid supply period");
        var overlaps = await db.SupplyPeriods.AnyAsync(period => period.MeteringPointId == id && period.Status != SupplyPeriodStatus.Cancelled &&
            (period.End == null || request.Start < period.End), cancellationToken);
        if (overlaps) return TypedResults.Problem(title: "Overlapping supply period", detail: "The metering point already has a non-cancelled supply period at that time. End the existing period first.", statusCode: 409);
        var period = new SupplyPeriod { MeteringPointId = id, CustomerId = request.CustomerId, Start = request.Start, Status = SupplyPeriodStatus.Active };
        db.SupplyPeriods.Add(period);
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.Created($"/api/v1/energy/metering-points/{id}/supply-periods/{period.Id}", SupplyPeriodResponse.FromDomain(period));
    }
}