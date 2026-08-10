using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.MeteringPoints;
using Vantigo.Energy.Domain.Meters;
using Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

namespace Vantigo.Energy.Endpoints.MeteringPoints;

internal static class CreateMeteringPointEndpoint
{
    internal static async Task<Results<CreatedAtRoute<MeteringPointResponse>, ValidationProblem, ProblemHttpResult>> Handler(
        MeteringPointRequest request, EnergyDbContext db, CancellationToken cancellationToken)
    {
        var errors = request.Validate();
        if (errors.Count > 0) return TypedResults.ValidationProblem(errors, title: "Invalid metering point");
        if (await db.MeteringPoints.AnyAsync(point => point.Gsrn == new Gsrn(request.Gsrn!), cancellationToken))
            return TypedResults.Problem(title: "Duplicate GSRN", detail: "A metering point with that GSRN already exists.", statusCode: 409);

        var point = request.ToDomain();
        point.Meters.Add(new Meter { MeterNumber = request.MeterNumber!.Trim(), InstalledAt = DateTimeOffset.UtcNow });
        db.MeteringPoints.Add(point);
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.CreatedAtRoute(MeteringPointResponse.FromDomain(point), MeteringPointsEndpoints.GetMeteringPointRouteName, new { id = point.Id });
    }
}