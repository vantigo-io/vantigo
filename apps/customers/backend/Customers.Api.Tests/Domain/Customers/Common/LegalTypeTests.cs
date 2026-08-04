using Vantigo.Customers.Api.Domain.Customers.Common;
using Vantigo.Customers.Api.Domain.Exceptions;

namespace Vantigo.Customers.Api.Tests.Domain.Customers.Common;

public sealed class LegalTypeTests
{
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Constructor_WithNullOrWhitespace_ThrowsDomainException(string? value)
    {
        Assert.Throws<DomainException>(() => new LegalType(value!));
    }

    [Fact]
    public void Constructor_TrimsAndLowercasesValue()
    {
        LegalType legalType = new(" Business ");

        Assert.Equal(LegalType.Business, (string)legalType);
    }

    [Theory]
    [InlineData(LegalType.Person)]
    [InlineData(LegalType.Business)]
    public void ImplicitConversion_FromWellKnownValues_CreatesLegalType(string value)
    {
        LegalType legalType = value;

        Assert.Equal(value, (string)legalType);
    }

    [Fact]
    public void TryCreate_WithValidValue_ReturnsTrueAndValue()
    {
        var success = LegalType.TryCreate("Business", out var result, out var error);

        Assert.True(success);
        Assert.Null(error);
        Assert.Equal(LegalType.Business, (string)result);
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void TryCreate_WithInvalidValue_ReturnsFalseAndError(string? value)
    {
        var success = LegalType.TryCreate(value, out _, out var error);

        Assert.False(success);
        Assert.NotNull(error);
    }
}