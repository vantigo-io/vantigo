using Vantigo.Customers.Api.Domain.Customers.Common;
using Vantigo.Customers.Api.Domain.Exceptions;

namespace Vantigo.Customers.Api.Tests.Domain.Customers.Common;

public sealed class LegalIdTests
{
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Constructor_WithNullOrWhitespace_ThrowsDomainException(string? value)
    {
        Assert.Throws<DomainException>(() => new LegalId(value!));
    }

    [Fact]
    public void Constructor_WithValueLongerThanMaxLength_ThrowsDomainException()
    {
        var value = new string('1', LegalId.MaxLength + 1);

        Assert.Throws<DomainException>(() => new LegalId(value));
    }

    [Fact]
    public void Constructor_WithValueAtMaxLength_Succeeds()
    {
        var value = new string('1', LegalId.MaxLength);

        LegalId legalId = new(value);

        Assert.Equal(value, (string)legalId);
    }

    [Fact]
    public void Constructor_TrimsAndLowercasesValue()
    {
        LegalId legalId = new(" 923609016-A ");

        Assert.Equal("923609016-a", (string)legalId);
    }

    [Fact]
    public void ImplicitConversion_FromString_CreatesLegalId()
    {
        LegalId legalId = "923609016";

        Assert.Equal("923609016", (string)legalId);
    }

    [Fact]
    public void TryCreate_WithValidValue_ReturnsTrueAndValue()
    {
        var success = LegalId.TryCreate("923609016", out var result, out var error);

        Assert.True(success);
        Assert.Null(error);
        Assert.Equal("923609016", (string)result);
    }

    [Fact]
    public void TryCreate_WithTooLongValue_ReturnsFalseAndError()
    {
        var success = LegalId.TryCreate(new string('1', LegalId.MaxLength + 1), out _, out var error);

        Assert.False(success);
        Assert.NotNull(error);
    }
}