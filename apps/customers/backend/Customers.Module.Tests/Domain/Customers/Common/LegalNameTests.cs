using Vantigo.Customers.Domain.Customers.Common;
using Vantigo.Customers.Domain.Exceptions;

namespace Vantigo.Customers.Module.Tests.Domain.Customers.Common;

public sealed class LegalNameTests
{
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Constructor_WithNullOrWhitespace_ThrowsDomainException(string? value)
    {
        Assert.Throws<DomainException>(() => new LegalName(value!));
    }

    [Fact]
    public void Constructor_WithValueLongerThanMaxLength_ThrowsDomainException()
    {
        var value = new string('a', LegalName.MaxLength + 1);

        Assert.Throws<DomainException>(() => new LegalName(value));
    }

    [Fact]
    public void Constructor_WithValueAtMaxLength_Succeeds()
    {
        var value = new string('a', LegalName.MaxLength);

        LegalName legalName = new(value);

        Assert.Equal(value, (string)legalName);
    }

    [Fact]
    public void Constructor_TrimsValueButPreservesCasing()
    {
        LegalName legalName = new(" Acme AS ");

        Assert.Equal("Acme AS", (string)legalName);
    }

    [Fact]
    public void ImplicitConversion_FromString_CreatesLegalName()
    {
        LegalName legalName = "Acme AS";

        Assert.Equal("Acme AS", (string)legalName);
    }

    [Fact]
    public void TryCreate_WithValidValue_ReturnsTrueAndValue()
    {
        var success = LegalName.TryCreate("Acme AS", out var result, out var error);

        Assert.True(success);
        Assert.Null(error);
        Assert.Equal("Acme AS", (string)result);
    }

    [Fact]
    public void TryCreate_WithTooLongValue_ReturnsFalseAndError()
    {
        var success = LegalName.TryCreate(new string('a', LegalName.MaxLength + 1), out _, out var error);

        Assert.False(success);
        Assert.NotNull(error);
    }
}