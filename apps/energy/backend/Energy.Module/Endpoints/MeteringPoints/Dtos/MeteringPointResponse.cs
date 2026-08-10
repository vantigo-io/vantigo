using Vantigo.Energy.Domain.MeteringPoints;

namespace Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

internal sealed record MeteringPointResponse(
    int Id,
    string Gsrn,
    string? MeterNumber,
    AddressResponse Address,
    string PriceArea,
    string? GridArea,
    decimal? ExpectedAnnualConsumptionKwh,
    double? Latitude,
    double? Longitude,
    string ConnectionStatus,
    DateTimeOffset CreatedAt,
    DateTimeOffset UpdatedAt)
{
    internal static MeteringPointResponse FromDomain(MeteringPoint point) => new(
        point.Id, point.Gsrn.Value, point.Meters.FirstOrDefault(meter => meter.RemovedAt is null)?.MeterNumber,
        new AddressResponse(point.Address.StreetAddress, point.Address.PostalCode, point.Address.City, point.Address.CountryCode),
        point.PriceArea, point.GridArea, point.ExpectedAnnualConsumptionKwh, point.Latitude, point.Longitude,
        point.ConnectionStatus.ToString(), point.CreatedAt, point.UpdatedAt);
}

internal sealed record AddressResponse(string StreetAddress, string PostalCode, string City, string CountryCode);