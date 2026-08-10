using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

namespace Vantigo.Energy.Endpoints.MeteringPoints;

internal static class UpdateMeteringPointEndpoint
{
    internal static async Task<Results<Ok<MeteringPointResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id, MeteringPointUpdateRequest request, EnergyDbContext db, CancellationToken cancellationToken)
    {
        var errors = request.Validate();
        if (errors.Count > 0) return TypedResults.ValidationProblem(errors, title: "Invalid metering point");
        var point = await db.MeteringPoints.Include(item => item.Meters).SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (point is null) return TypedResults.NotFound();
        if (await db.MeteringPoints.AnyAsync(item => item.Id != id && item.Gsrn == new Domain.MeteringPoints.Gsrn(request.Gsrn!), cancellationToken))
            return TypedResults.Problem(title: "Duplicate GSRN", detail: "A metering point with that GSRN already exists.", statusCode: 409);
        point.Gsrn = new Domain.MeteringPoints.Gsrn(request.Gsrn!);
        point.Address.Update(request.Address!.StreetAddress!, request.Address.PostalCode!, request.Address.City!, request.Address.CountryCode ?? "NO");
        point.PriceArea = request.PriceArea!;
        point.GridArea = string.IsNullOrWhiteSpace(request.GridArea) ? null : request.GridArea.Trim();
        point.ExpectedAnnualConsumptionKwh = request.ExpectedAnnualConsumptionKwh;
        point.Latitude = request.Latitude;
        point.Longitude = request.Longitude;
        point.ConnectionStatus = request.ConnectionStatus is null
            ? Domain.MeteringPoints.ConnectionStatus.New
            : Enum.Parse<Domain.MeteringPoints.ConnectionStatus>(request.ConnectionStatus, true);
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.Ok(MeteringPointResponse.FromDomain(point));
    }
}