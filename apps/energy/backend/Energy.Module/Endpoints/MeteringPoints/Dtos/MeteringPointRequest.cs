using Vantigo.Energy.Domain.MeteringPoints;
using Vantigo.Energy.Domain.Meters;

namespace Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

internal sealed record MeteringPointRequest(
    string? Gsrn,
    string? MeterNumber,
    AddressRequest? Address,
    string? PriceArea,
    string? GridArea,
    decimal? ExpectedAnnualConsumptionKwh,
    double? Latitude,
    double? Longitude,
    string? ConnectionStatus)
{
    internal Dictionary<string, string[]> Validate()
    {
        var errors = new Dictionary<string, string[]>(StringComparer.Ordinal);
        if (!Gsrn.IsNullOrValidGsrn()) errors["gsrn"] = ["GSRN must contain exactly 18 digits."];
        if (Meter.ValidateMeterNumber(MeterNumber) is { } meterError) errors["meterNumber"] = [meterError];
        if (Address is null) errors["address"] = ["Address is required."];
        else
        {
            AddAddressError(errors, "streetAddress", Address.StreetAddress, 200);
            AddAddressError(errors, "postalCode", Address.PostalCode, 16);
            AddAddressError(errors, "city", Address.City, 100);
            if (Address.CountryCode is not null && (Address.CountryCode.Length != 2 || !Address.CountryCode.All(char.IsLetter)))
                errors["address.countryCode"] = ["Country code must contain two letters."];
        }
        if (MeteringPoint.ValidatePriceArea(PriceArea) is { } areaError) errors["priceArea"] = [areaError];
        if (MeteringPoint.ValidateExpectedConsumption(ExpectedAnnualConsumptionKwh) is { } consumptionError)
            errors["expectedAnnualConsumptionKwh"] = [consumptionError];
        if (MeteringPoint.ValidateLocation(Latitude, Longitude) is { } locationError)
            errors["location"] = [locationError];
        if (ConnectionStatus is not null && !Enum.TryParse<ConnectionStatus>(ConnectionStatus, true, out _))
            errors["connectionStatus"] = ["Connection status must be New, Connected or Disconnected."];
        return errors;
    }

    internal MeteringPoint ToDomain() => new()
    {
        Gsrn = new Gsrn(Gsrn!),
        Address = new Address(Address!.StreetAddress!, Address.PostalCode!, Address.City!, Address.CountryCode ?? "NO"),
        PriceArea = PriceArea!,
        GridArea = string.IsNullOrWhiteSpace(GridArea) ? null : GridArea.Trim(),
        ExpectedAnnualConsumptionKwh = ExpectedAnnualConsumptionKwh,
        Latitude = Latitude,
        Longitude = Longitude,
        ConnectionStatus = ConnectionStatus is null ? global::Vantigo.Energy.Domain.MeteringPoints.ConnectionStatus.New : Enum.Parse<global::Vantigo.Energy.Domain.MeteringPoints.ConnectionStatus>(ConnectionStatus, true),
    };

    private static void AddAddressError(Dictionary<string, string[]> errors, string field, string? value, int maxLength)
    {
        if (string.IsNullOrWhiteSpace(value)) errors[$"address.{field}"] = ["This field is required."];
        else if (value.Trim().Length > maxLength) errors[$"address.{field}"] = [$"This field cannot be longer than {maxLength} characters."];
    }
}

internal sealed record MeteringPointUpdateRequest(
    string? Gsrn,
    AddressRequest? Address,
    string? PriceArea,
    string? GridArea,
    decimal? ExpectedAnnualConsumptionKwh,
    double? Latitude,
    double? Longitude,
    string? ConnectionStatus)
{
    internal Dictionary<string, string[]> Validate()
    {
        var errors = new Dictionary<string, string[]>(StringComparer.Ordinal);
        if (!Gsrn.IsNullOrValidGsrn()) errors["gsrn"] = ["GSRN must contain exactly 18 digits."];
        if (Address is null) errors["address"] = ["Address is required."];
        else
        {
            AddAddressError(errors, "streetAddress", Address.StreetAddress, 200);
            AddAddressError(errors, "postalCode", Address.PostalCode, 16);
            AddAddressError(errors, "city", Address.City, 100);
            if (Address.CountryCode is not null && (Address.CountryCode.Length != 2 || !Address.CountryCode.All(char.IsLetter)))
                errors["address.countryCode"] = ["Country code must contain two letters."];
        }
        if (MeteringPoint.ValidatePriceArea(PriceArea) is { } areaError) errors["priceArea"] = [areaError];
        if (MeteringPoint.ValidateExpectedConsumption(ExpectedAnnualConsumptionKwh) is { } consumptionError)
            errors["expectedAnnualConsumptionKwh"] = [consumptionError];
        if (MeteringPoint.ValidateLocation(Latitude, Longitude) is { } locationError)
            errors["location"] = [locationError];
        if (ConnectionStatus is not null && !Enum.TryParse<ConnectionStatus>(ConnectionStatus, true, out _))
            errors["connectionStatus"] = ["Connection status must be New, Connected or Disconnected."];
        return errors;
    }

    private static void AddAddressError(Dictionary<string, string[]> errors, string field, string? value, int maxLength)
    {
        if (string.IsNullOrWhiteSpace(value)) errors[$"address.{field}"] = ["This field is required."];
        else if (value.Trim().Length > maxLength) errors[$"address.{field}"] = [$"This field cannot be longer than {maxLength} characters."];
    }
}

internal sealed record AddressRequest(string? StreetAddress, string? PostalCode, string? City, string? CountryCode);

internal static class MeteringPointRequestExtensions
{
    internal static bool IsNullOrValidGsrn(this string? value) => value is not null && Gsrn.IsValid(value);
}