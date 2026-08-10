using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

namespace Vantigo.Energy.Endpoints.MeteringPoints;

internal static class UpdateMeteringPointEndpoint
{
    internal static async Task<Results<Ok<MeteringPointResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id, MeteringPointRequest request, EnergyDbContext db, CancellationToken cancellationToken)
    {
        var errors = request.Validate();
        if (errors.Count > 0) return TypedResults.ValidationProblem(errors, title: "Invalid metering point");
        var point = await db.MeteringPoints.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (point is null) return TypedResults.NotFound();
        if (await db.MeteringPoints.AnyAsync(item => item.Id != id && item.Gsrn == new Domain.MeteringPoints.Gsrn(request.Gsrn!), cancellationToken))
            return TypedResults.Problem(title: "Duplicate GSRN", detail: "A metering point with that GSRN already exists.", statusCode: 409);
        var replacement = request.ToDomain();
        point.Gsrn = replacement.Gsrn;
        point.MeterNumber = replacement.MeterNumber;
        point.Address.Update(replacement.Address.StreetAddress, replacement.Address.PostalCode, replacement.Address.City, replacement.Address.CountryCode);
        point.PriceArea = replacement.PriceArea;
        point.GridArea = replacement.GridArea;
        point.ExpectedAnnualConsumptionKwh = replacement.ExpectedAnnualConsumptionKwh;
        point.Latitude = replacement.Latitude;
        point.Longitude = replacement.Longitude;
        point.ConnectionStatus = replacement.ConnectionStatus;
        await db.SaveChangesAsync(cancellationToken);
        return TypedResults.Ok(MeteringPointResponse.FromDomain(point));
    }
}