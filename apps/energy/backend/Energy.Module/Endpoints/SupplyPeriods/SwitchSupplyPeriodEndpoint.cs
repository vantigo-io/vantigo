using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts;
using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.SupplyPeriods;
using Vantigo.Energy.Endpoints.SupplyPeriods.Dtos;

namespace Vantigo.Energy.Endpoints.SupplyPeriods;

internal static class SwitchSupplyPeriodEndpoint
{
    internal static async Task<Results<Created<SwitchSupplyPeriodResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id, SwitchSupplyPeriodRequest request, EnergyDbContext db, ICustomerDirectory customerDirectory,
        CancellationToken cancellationToken)
    {
        if (!await db.MeteringPoints.AnyAsync(point => point.Id == id, cancellationToken)) return TypedResults.NotFound();
        if (request.CustomerId <= 0)
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["customerId"] = ["Customer ID must be greater than zero."] },
                title: "Invalid supply period");
        if (SupplyPeriod.Validate(request.SwitchAt, null) is { } validation)
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["switchAt"] = [validation] }, title: "Invalid supply period");
        if (await customerDirectory.FindCustomerAsync(request.CustomerId, cancellationToken) is null)
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["customerId"] = [$"Customer {request.CustomerId} does not exist."] },
                title: "Invalid supply period");

        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        var active = await db.SupplyPeriods.FirstOrDefaultAsync(period =>
            period.MeteringPointId == id && period.Status == SupplyPeriodStatus.Active && period.End == null, cancellationToken);

        if (active is not null)
        {
            if (request.CustomerId == active.CustomerId)
                return TypedResults.ValidationProblem(
                    new Dictionary<string, string[]> { ["customerId"] = ["The customer is already the active customer."] },
                    title: "Invalid supply period");
            if (request.SwitchAt <= active.Start)
                return TypedResults.ValidationProblem(
                    new Dictionary<string, string[]> { ["switchAt"] = ["Switch date must be after the active period's start."] },
                    title: "Invalid supply period");

            active.End = request.SwitchAt;
            active.Status = SupplyPeriodStatus.Ended;
        }
        else
        {
            var overlaps = await db.SupplyPeriods.AnyAsync(period => period.MeteringPointId == id &&
                period.Status != SupplyPeriodStatus.Cancelled && (period.End == null || request.SwitchAt < period.End), cancellationToken);
            if (overlaps)
                return TypedResults.Problem(
                    title: "Overlapping supply period",
                    detail: "The metering point already has a non-cancelled supply period at that time. End the existing period first.",
                    statusCode: 409);
        }

        var newPeriod = new SupplyPeriod
        {
            MeteringPointId = id,
            CustomerId = request.CustomerId,
            Start = request.SwitchAt,
            Status = SupplyPeriodStatus.Active,
        };
        db.SupplyPeriods.Add(newPeriod);
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);

        return TypedResults.Created(
            $"/api/v1/energy/metering-points/{id}/supply-periods/{newPeriod.Id}",
            new SwitchSupplyPeriodResponse(
                active is null ? null : SupplyPeriodResponse.FromDomain(active),
                SupplyPeriodResponse.FromDomain(newPeriod)));
    }
}