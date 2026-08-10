using Vantigo.Customers.Domain.Customers.Common;
using Vantigo.Customers.Domain.Exceptions;

namespace Vantigo.Customers.Module.Tests.Domain.Customers.Common;

public sealed class CountryCodeTests
{
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Constructor_WithNullOrWhitespace_ThrowsDomainException(string? value)
    {
        Assert.Throws<DomainException>(() => new CountryCode(value!));
    }

    [Fact]
    public void Constructor_TrimsAndLowercasesValue()
    {
        CountryCode countryCode = new(" NO ");

        Assert.Equal("no", (string)countryCode);
    }

    [Fact]
    public void ImplicitConversion_FromString_CreatesCountryCode()
    {
        CountryCode countryCode = "se";

        Assert.Equal("se", (string)countryCode);
    }

    [Fact]
    public void TryCreate_WithValidValue_ReturnsTrueAndValue()
    {
        var success = CountryCode.TryCreate("NO", out var result, out var error);

        Assert.True(success);
        Assert.Null(error);
        Assert.Equal("no", (string)result);
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void TryCreate_WithInvalidValue_ReturnsFalseAndError(string? value)
    {
        var success = CountryCode.TryCreate(value, out _, out var error);

        Assert.False(success);
        Assert.NotNull(error);
    }
}