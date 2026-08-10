using Vantigo.Energy.Domain.Exceptions;

namespace Vantigo.Energy.Domain.MeteringPoints;

public sealed class Address
{
    public const int StreetAddressMaxLength = 200;
    public const int PostalCodeMaxLength = 16;
    public const int CityMaxLength = 100;

    private Address() { }

    public Address(string streetAddress, string postalCode, string city, string countryCode = "NO")
    {
        StreetAddress = Validate(streetAddress, StreetAddressMaxLength, "Street address");
        PostalCode = Validate(postalCode, PostalCodeMaxLength, "Postal code");
        City = Validate(city, CityMaxLength, "City");
        CountryCode = ValidateCountry(countryCode);
    }

    public string StreetAddress { get; private set; } = string.Empty;
    public string PostalCode { get; private set; } = string.Empty;
    public string City { get; private set; } = string.Empty;
    public string CountryCode { get; private set; } = "NO";

    public void Update(string streetAddress, string postalCode, string city, string? countryCode = null)
    {
        StreetAddress = Validate(streetAddress, StreetAddressMaxLength, "Street address");
        PostalCode = Validate(postalCode, PostalCodeMaxLength, "Postal code");
        City = Validate(city, CityMaxLength, "City");
        CountryCode = ValidateCountry(countryCode ?? CountryCode);
    }

    private static string Validate(string? value, int maxLength, string name) =>
        string.IsNullOrWhiteSpace(value)
            ? throw new DomainException($"{name} is required.")
            : value.Trim() is var trimmed && trimmed.Length <= maxLength
                ? trimmed
                : throw new DomainException($"{name} cannot be longer than {maxLength} characters.");

    private static string ValidateCountry(string? value)
    {
        var country = Validate(value, 2, "Country code").ToUpperInvariant();
        if (country.Length != 2 || country.Any(character => character is < 'A' or > 'Z'))
            throw new DomainException("Country code must contain two letters.");
        return country;
    }
}