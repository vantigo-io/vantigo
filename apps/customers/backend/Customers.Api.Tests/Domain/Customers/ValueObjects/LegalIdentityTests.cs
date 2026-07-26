using Vantigo.Customers.Api.Domain.Customers.Common;
using Vantigo.Customers.Api.Domain.Customers.ValueObjects;

namespace Vantigo.Customers.Api.Tests.Domain.Customers.ValueObjects;

public sealed class LegalIdentityTests
{
    [Fact]
    public void Construction_WithValidValues_ExposesNormalizedValues()
    {
        var identity = new LegalIdentity
        {
            Country = "NO",
            Type = "Business",
            Id = "923609016",
            Name = "Acme AS",
        };

        Assert.Equal("no", (string)identity.Country);
        Assert.Equal(LegalType.Business, (string)identity.Type);
        Assert.Equal("923609016", (string)identity.Id);
        Assert.Equal("Acme AS", (string)identity.Name);
    }

    [Fact]
    public void Equality_WithSameValues_AreEqual()
    {
        var first = new LegalIdentity { Country = "no", Type = "business", Id = "923609016", Name = "Acme AS" };
        var second = new LegalIdentity { Country = "NO", Type = "Business", Id = "923609016", Name = "Acme AS" };

        Assert.Equal(first, second);
    }

    [Fact]
    public void TryCreate_WithValidValues_ReturnsTrueAndIdentity()
    {
        var success = LegalIdentity.TryCreate("NO", "Business", "923609016", "Acme AS", out var identity, out var errors);

        Assert.True(success);
        Assert.Empty(errors);
        Assert.Equal("no", (string)identity.Country);
        Assert.Equal("business", (string)identity.Type);
        Assert.Equal("923609016", (string)identity.Id);
        Assert.Equal("Acme AS", (string)identity.Name);
    }

    [Fact]
    public void TryCreate_WithMultipleInvalidValues_ReportsAllErrorsAtOnce()
    {
        var success = LegalIdentity.TryCreate("", "business", "  ", "Acme AS", out _, out var errors);

        Assert.False(success);
        Assert.Equal(2, errors.Count);
        Assert.Contains("country", errors.Keys);
        Assert.Contains("id", errors.Keys);
    }

    [Fact]
    public void TryCreate_WithAllValuesInvalid_ReportsAnErrorPerField()
    {
        var success = LegalIdentity.TryCreate(null, null, null, null, out _, out var errors);

        Assert.False(success);
        Assert.Equal(["country", "id", "name", "type"], errors.Keys.Order());
    }
}
