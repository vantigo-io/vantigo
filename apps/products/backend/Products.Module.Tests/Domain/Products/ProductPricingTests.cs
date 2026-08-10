using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Module.Tests.Domain.Products;

public sealed class ProductPricingTests
{
    private static readonly DateTimeOffset Now = new(2026, 8, 9, 12, 0, 0, TimeSpan.Zero);

    [Fact]
    public void EffectivePrice_WithOnlyBasePrice_ReturnsBasePrice()
    {
        var prices = new[] { Base("NOK", 599m) };

        var effective = ProductPricing.GetEffectivePrice(prices, "NOK", Now);

        Assert.NotNull(effective);
        Assert.Equal(599m, effective.Amount);
    }

    [Fact]
    public void EffectivePrice_WithActiveCampaign_PrefersCampaignOverBase()
    {
        var prices = new[]
        {
            Base("NOK", 599m),
            Campaign("NOK", 499m, Now.AddDays(-1), Now.AddDays(1)),
        };

        var effective = ProductPricing.GetEffectivePrice(prices, "NOK", Now);

        Assert.NotNull(effective);
        Assert.Equal(499m, effective.Amount);
    }

    [Fact]
    public void EffectivePrice_WithExpiredCampaign_FallsBackToBase()
    {
        var prices = new[]
        {
            Base("NOK", 599m),
            Campaign("NOK", 499m, Now.AddDays(-10), Now.AddDays(-5)),
        };

        var effective = ProductPricing.GetEffectivePrice(prices, "NOK", Now);

        Assert.NotNull(effective);
        Assert.Equal(599m, effective.Amount);
    }

    [Fact]
    public void EffectivePrice_WithFutureCampaign_FallsBackToBase()
    {
        var prices = new[]
        {
            Base("NOK", 599m),
            Campaign("NOK", 499m, Now.AddDays(5), Now.AddDays(10)),
        };

        var effective = ProductPricing.GetEffectivePrice(prices, "NOK", Now);

        Assert.NotNull(effective);
        Assert.Equal(599m, effective.Amount);
    }

    [Fact]
    public void EffectivePrice_WithNoPrices_ReturnsNull()
    {
        Assert.Null(ProductPricing.GetEffectivePrice([], "NOK", Now));
    }

    [Fact]
    public void EffectivePrice_WithOtherCurrencyOnly_ReturnsNull()
    {
        Assert.Null(ProductPricing.GetEffectivePrice([Base("SEK", 649m)], "NOK", Now));
    }

    [Fact]
    public void EffectivePrices_ReturnsOnePricePerCurrency()
    {
        var prices = new[]
        {
            Base("NOK", 599m),
            Base("SEK", 649m),
            Campaign("NOK", 499m, Now.AddDays(-1), Now.AddDays(1)),
        };

        var effective = ProductPricing.GetEffectivePrices(prices, Now);

        Assert.Equal(2, effective.Count);
        Assert.Equal(499m, effective.Single(price => price.Currency == "NOK").Amount);
        Assert.Equal(649m, effective.Single(price => price.Currency == "SEK").Amount);
    }

    [Fact]
    public void EffectivePrice_WithOverlappingCampaigns_PrefersLatestStart()
    {
        var prices = new[]
        {
            Campaign("NOK", 549m, Now.AddDays(-10), Now.AddDays(10)),
            Campaign("NOK", 449m, Now.AddDays(-1), Now.AddDays(1)),
        };

        var effective = ProductPricing.GetEffectivePrice(prices, "NOK", Now);

        Assert.NotNull(effective);
        Assert.Equal(449m, effective.Amount);
    }

    [Fact]
    public void Conflicts_TwoBasePricesSameCurrency_Conflict()
    {
        Assert.True(ProductPricing.Conflicts(Base("NOK", 599m), Base("NOK", 649m)));
    }

    [Fact]
    public void Conflicts_BasePricesInDifferentCurrencies_DoNotConflict()
    {
        Assert.False(ProductPricing.Conflicts(Base("NOK", 599m), Base("SEK", 649m)));
    }

    [Fact]
    public void Conflicts_CampaignOverBase_DoesNotConflict()
    {
        Assert.False(ProductPricing.Conflicts(
            Campaign("NOK", 499m, Now.AddDays(-1), Now.AddDays(1)),
            Base("NOK", 599m)));
    }

    [Fact]
    public void Conflicts_OverlappingCampaignsSameCurrency_Conflict()
    {
        Assert.True(ProductPricing.Conflicts(
            Campaign("NOK", 499m, Now.AddDays(-1), Now.AddDays(5)),
            Campaign("NOK", 449m, Now.AddDays(2), Now.AddDays(10))));
    }

    [Fact]
    public void Conflicts_DisjointCampaignsSameCurrency_DoNotConflict()
    {
        Assert.False(ProductPricing.Conflicts(
            Campaign("NOK", 499m, Now.AddDays(-10), Now.AddDays(-5)),
            Campaign("NOK", 449m, Now.AddDays(5), Now.AddDays(10))));
    }

    private static ProductPrice Base(string currency, decimal amount) => new()
    {
        Currency = currency,
        Amount = amount,
    };

    private static ProductPrice Campaign(
        string currency,
        decimal amount,
        DateTimeOffset from,
        DateTimeOffset to) => new()
        {
            Currency = currency,
            Amount = amount,
            ValidFrom = from,
            ValidTo = to,
        };
}