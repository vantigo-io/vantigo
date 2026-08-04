using Vantigo.Customers.Api.Domain.Customers.Common;
using Vantigo.Customers.Api.Domain.Exceptions;

namespace Vantigo.Customers.Api.Tests.Domain.Customers.Common;

public sealed class FriendlyNameTests
{
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Constructor_WithNullOrWhitespace_ThrowsDomainException(string? value)
    {
        Assert.Throws<DomainException>(() => new FriendlyName(value!));
    }

    [Fact]
    public void Constructor_WithValueLongerThanMaxLength_ThrowsDomainException()
    {
        var value = new string('a', FriendlyName.MaxLength + 1);

        Assert.Throws<DomainException>(() => new FriendlyName(value));
    }

    [Fact]
    public void Constructor_WithValueAtMaxLength_Succeeds()
    {
        var value = new string('a', FriendlyName.MaxLength);

        FriendlyName friendlyName = new(value);

        Assert.Equal(value, (string)friendlyName);
    }

    [Fact]
    public void Constructor_TrimsValueButPreservesCasing()
    {
        FriendlyName friendlyName = new(" Wayne Enterprises ");

        Assert.Equal("Wayne Enterprises", (string)friendlyName);
    }

    [Fact]
    public void TryCreate_WithValidValue_ReturnsTrueAndValue()
    {
        var success = FriendlyName.TryCreate("Acme", out var result, out var error);

        Assert.True(success);
        Assert.Null(error);
        Assert.Equal("Acme", (string)result);
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void TryCreate_WithInvalidValue_ReturnsFalseAndError(string? value)
    {
        var success = FriendlyName.TryCreate(value, out _, out var error);

        Assert.False(success);
        Assert.NotNull(error);
    }
}