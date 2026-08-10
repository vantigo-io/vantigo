using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.Meters;
using Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

namespace Vantigo.Energy.Endpoints.MeteringPoints;

internal static class ReplaceMeterEndpoint
{
    internal static async Task<Results<Created<MeterResponse>, NotFound, ValidationProblem>> Handler(
        int id, ReplaceMeterRequest request, EnergyDbContext db, CancellationToken cancellationToken)
    {
        if (!await db.MeteringPoints.AnyAsync(point => point.Id == id, cancellationToken)) return TypedResults.NotFound();
        var errors = request.Validate();
        if (errors.Count > 0) return TypedResults.ValidationProblem(errors, title: "Invalid meter");

        await using var transaction = await db.Database.BeginTransactionAsync(cancellationToken);
        var active = await db.Meters.SingleOrDefaultAsync(meter => meter.MeteringPointId == id && meter.RemovedAt == null, cancellationToken);
        var installedAt = request.InstalledAt!.Value;
        if (active is not null && installedAt <= active.InstalledAt)
        {
            return TypedResults.ValidationProblem(
                new Dictionary<string, string[]> { ["installedAt"] = ["Installed at must be after the active meter's installation time."] },
                title: "Invalid meter");
        }

        if (active is not null)
        {
            active.RemovedAt = installedAt;
            await db.SaveChangesAsync(cancellationToken);
        }
        var meter = new Meter { MeteringPointId = id, MeterNumber = request.MeterNumber!.Trim(), InstalledAt = installedAt };
        db.Meters.Add(meter);
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Created($"/api/v1/energy/metering-points/{id}/meters/{meter.Id}", MeterResponse.FromDomain(meter));
    }
}