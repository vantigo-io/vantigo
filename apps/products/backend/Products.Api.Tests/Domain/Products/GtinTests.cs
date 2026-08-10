using Vantigo.Products.Api.Domain.Products;

namespace Vantigo.Products.Api.Tests.Domain.Products;

public sealed class GtinTests
{
    [Theory]
    [InlineData("96385074")] // GTIN-8
    [InlineData("036000291452")] // GTIN-12 (UPC-A)
    [InlineData("4006381333931")] // GTIN-13 (EAN-13)
    [InlineData("7350053850019")] // GTIN-13
    [InlineData("00012345600012")] // GTIN-14
    public void IsValid_WithCorrectCheckDigit_ReturnsTrue(string value)
    {
        Assert.True(Gtin.IsValid(value));
    }

    [Theory]
    [InlineData("96385075")] // wrong check digit
    [InlineData("4006381333932")] // wrong check digit
    [InlineData("00012345600013")] // wrong check digit
    public void IsValid_WithWrongCheckDigit_ReturnsFalse(string value)
    {
        Assert.False(Gtin.IsValid(value));
    }

    [Theory]
    [InlineData("")]
    [InlineData("1234567")] // 7 digits
    [InlineData("123456789")] // 9 digits
    [InlineData("12345678901")] // 11 digits
    [InlineData("123456789012345")] // 15 digits
    public void IsValid_WithUnsupportedLength_ReturnsFalse(string value)
    {
        Assert.False(Gtin.IsValid(value));
    }

    [Theory]
    [InlineData("9638507a")]
    [InlineData("4006381 33393")]
    [InlineData("40063813339-1")]
    public void IsValid_WithNonDigitCharacters_ReturnsFalse(string value)
    {
        Assert.False(Gtin.IsValid(value));
    }
}