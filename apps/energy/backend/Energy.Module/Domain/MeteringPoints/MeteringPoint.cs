using Vantigo.Energy.Domain.Exceptions;

namespace Vantigo.Energy.Domain.MeteringPoints;

public sealed class MeteringPoint
{
    public int Id { get; set; }
    public Gsrn Gsrn { get; set; }
    public string MeterNumber { get; set; } = string.Empty;
    public Address Address { get; set; } = new("Unknown", "0000", "Unknown");
    public string PriceArea { get; set; } = string.Empty;
    public string? GridArea { get; set; }
    public decimal? ExpectedAnnualConsumptionKwh { get; set; }
    public double? Latitude { get; set; }
    public double? Longitude { get; set; }
    public ConnectionStatus ConnectionStatus { get; set; } = ConnectionStatus.New;
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
    public List<Consumption.ConsumptionInterval> ConsumptionIntervals { get; set; } = [];
    public List<SupplyPeriods.SupplyPeriod> SupplyPeriods { get; set; } = [];

    public static string? ValidateLocation(double? latitude, double? longitude)
    {
        if (latitude is < -90 or > 90) return "Latitude must be between -90 and 90.";
        if (longitude is < -180 or > 180) return "Longitude must be between -180 and 180.";
        return null;
    }

    public static string? ValidateMeterNumber(string? value) => string.IsNullOrWhiteSpace(value)
        ? "Meter number is required."
        : value.Trim().Length > 64 ? "Meter number cannot be longer than 64 characters." : null;

    public static string? ValidatePriceArea(string? value) =>
        global::Vantigo.Energy.Domain.MeteringPoints.PriceArea.IsValid(value) ? null : "Price area must contain two uppercase letters followed by one or two digits.";

    public static string? ValidateExpectedConsumption(decimal? value) =>
        value is < 0 ? "Expected annual consumption cannot be negative." : null;

    public void Validate()
    {
        if (!Gsrn.IsValid(Gsrn.Value)) throw new DomainException("A GSRN must contain exactly 18 digits.");
        if (ValidateMeterNumber(MeterNumber) is { } meterError) throw new DomainException(meterError);
        if (ValidatePriceArea(PriceArea) is { } areaError) throw new DomainException(areaError);
        if (ValidateLocation(Latitude, Longitude) is { } locationError) throw new DomainException(locationError);
        if (ValidateExpectedConsumption(ExpectedAnnualConsumptionKwh) is { } consumptionError) throw new DomainException(consumptionError);
    }
}