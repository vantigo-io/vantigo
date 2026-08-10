using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.SupplyPeriods;
using Vantigo.Energy.Endpoints.MeteringPoints.Dtos;
using Vantigo.Energy.Endpoints.SupplyPeriods.Dtos;

namespace Vantigo.Energy.Endpoints.Customers;

internal sealed record CustomerMeteringPointResponse(MeteringPointResponse MeteringPoint, IReadOnlyList<SupplyPeriodResponse> SupplyPeriods);

internal static class GetCustomerMeteringPointsEndpoint
{
    internal static async Task<Ok<IReadOnlyList<CustomerMeteringPointResponse>>> Handler(int customerId, EnergyDbContext db, CancellationToken cancellationToken)
    {
        var points = await db.MeteringPoints.AsNoTracking()
            .Where(point => point.SupplyPeriods.Any(period => period.CustomerId == customerId && period.Status != SupplyPeriodStatus.Cancelled))
            .OrderBy(point => point.Id).Select(point => new CustomerMeteringPointResponse(
                new MeteringPointResponse(
                    point.Id, point.Gsrn.Value, point.Meters.Where(meter => meter.RemovedAt == null).Select(meter => meter.MeterNumber).FirstOrDefault(),
                    new AddressResponse(point.Address.StreetAddress, point.Address.PostalCode, point.Address.City, point.Address.CountryCode),
                    point.PriceArea, point.GridArea, point.ExpectedAnnualConsumptionKwh, point.Latitude, point.Longitude,
                    point.ConnectionStatus.ToString(), point.CreatedAt, point.UpdatedAt),
                point.SupplyPeriods
                    .Where(period => period.CustomerId == customerId && period.Status != SupplyPeriodStatus.Cancelled)
                    .OrderBy(period => period.Start)
                    .Select(period => new SupplyPeriodResponse(period.Id, period.MeteringPointId, period.CustomerId, period.Start, period.End, period.Status.ToString()))
                    .ToList()))
            .ToListAsync(cancellationToken);
        return TypedResults.Ok<IReadOnlyList<CustomerMeteringPointResponse>>(points);
    }
}