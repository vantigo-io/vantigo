using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Products;
using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Products.Module.Tests.Integration;

[Collection(ProductsModuleCollection.Name)]
public sealed class ProductCatalogContractTests
{
    private readonly ProductsModuleFactory _factory;

    public ProductCatalogContractTests(ProductsModuleFactory factory) => _factory = factory;

    [Fact]
    public async Task Search_returns_only_active_products_and_clamps_take()
    {
        var marker = Marker();
        var active = await AddProductAsync($"Active {marker}", ProductStatus.Active, sku: $"ACTIVE-{marker}");
        await AddProductAsync($"Draft {marker}", ProductStatus.Draft, sku: $"DRAFT-{marker}");
        await AddProductAsync($"Discontinued {marker}", ProductStatus.Discontinued, sku: $"DISCONTINUED-{marker}");

        var results = await CatalogAsync(catalog => catalog.SearchAsync(marker.ToLowerInvariant(), 0));

        var result = Assert.Single(results);
        Assert.Equal(active.Id, result.Id);
        Assert.Equal("Goods", result.Type);
        Assert.Equal(active.Variants.Single().Sku, Assert.Single(result.Variants).Sku);
    }

    [Fact]
    public async Task Search_matches_name_sku_barcode_and_category_case_insensitively()
    {
        var nameMarker = Marker();
        var skuMarker = Marker();
        var barcodeMarker = NewGtin13();
        var categoryMarker = $"Category-{Marker()}";
        var category = await AddCategoryAsync(categoryMarker);
        var nameProduct = await AddProductAsync($"Name-{nameMarker}", ProductStatus.Active, sku: $"SKU-{Marker()}");
        var skuProduct = await AddProductAsync($"Product-{Marker()}", ProductStatus.Active, sku: $"Sku-{skuMarker}");
        var barcodeProduct = await AddProductAsync($"Product-{Marker()}", ProductStatus.Active, sku: $"SKU-{Marker()}", barcode: barcodeMarker);
        var categoryProduct = await AddProductAsync($"Product-{Marker()}", ProductStatus.Active, sku: $"SKU-{Marker()}", categoryId: category.Id);

        Assert.Equal(nameProduct.Id, Assert.Single(await SearchAsync(nameMarker.ToUpperInvariant())).Id);
        Assert.Equal(skuProduct.Id, Assert.Single(await SearchAsync(skuMarker.ToUpperInvariant())).Id);
        Assert.Equal(barcodeProduct.Id, Assert.Single(await SearchAsync(barcodeMarker)).Id);
        Assert.Equal(categoryProduct.Id, Assert.Single(await SearchAsync(categoryMarker.ToLowerInvariant())).Id);
    }

    [Fact]
    public async Task Search_is_bounded_to_ten_results_and_rejects_blank_query()
    {
        var marker = Marker();
        for (var index = 0; index < 12; index++)
        {
            await AddProductAsync($"{marker}-{index:00}", ProductStatus.Active, sku: $"SKU-{marker}-{index:00}");
        }

        var results = await CatalogAsync(catalog => catalog.SearchAsync(marker, 100));

        Assert.Equal(10, results.Count);
        await Assert.ThrowsAsync<ArgumentException>(() => SearchAsync("  "));
    }

    [Fact]
    public async Task GetById_returns_safe_product_projection_with_effective_price_but_no_cost()
    {
        var product = await AddProductAsync(
            $"Full-{Marker()}",
            ProductStatus.Active,
            sku: $"SKU-{Marker()}",
            barcode: NewGtin13(),
            description: "A customer-facing description.",
            standardCost: 7.25m,
            price: new ProductPrice { Currency = "NOK", Amount = 19.99m });

        var result = await CatalogAsync(catalog => catalog.GetByIdAsync(product.Id));

        Assert.NotNull(result);
        Assert.Equal(product.Id, result.Id);
        Assert.Equal("A customer-facing description.", result.Description);
        var variant = Assert.Single(result.Variants);
        Assert.Equal(product.Variants.Single().Sku, variant.Sku);
        Assert.Equal(product.Variants.Single().Barcode, variant.Barcode);
        var price = Assert.Single(variant.Prices);
        Assert.Equal("NOK", price.Currency);
        Assert.Equal(19.99m, price.Amount);
        Assert.DoesNotContain(typeof(ProductCatalogProduct).GetProperties(), property =>
            property.Name.Contains("Cost", StringComparison.OrdinalIgnoreCase));
        Assert.DoesNotContain(typeof(ProductCatalogVariant).GetProperties(), property =>
            property.Name.Contains("Cost", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public async Task GetById_returns_null_for_missing_draft_and_discontinued_products()
    {
        var draft = await AddProductAsync($"Draft-{Marker()}", ProductStatus.Draft, sku: $"SKU-{Marker()}");
        var discontinued = await AddProductAsync($"Discontinued-{Marker()}", ProductStatus.Discontinued, sku: $"SKU-{Marker()}");

        var results = await CatalogAsync(async catalog =>
            (
                await catalog.GetByIdAsync(999_999_999),
                await catalog.GetByIdAsync(draft.Id),
                await catalog.GetByIdAsync(discontinued.Id)));

        Assert.Null(results.Item1);
        Assert.Null(results.Item2);
        Assert.Null(results.Item3);
    }

    [Fact]
    public async Task Catalog_honors_cancellation()
    {
        using var cancellation = new CancellationTokenSource();
        cancellation.Cancel();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() =>
            CatalogAsync(catalog => catalog.SearchAsync(Marker(), 1, cancellation.Token)));
    }

    [Fact]
    public async Task Catalog_isolates_products_and_allows_the_same_sku_per_tenant()
    {
        var sku = $"TENANT-{Marker()}";
        var tenantA = TenantId.New();
        var tenantB = TenantId.New();
        var productA = await AddTenantProductAsync(tenantA, $"Tenant A {Marker()}", sku);
        var productB = await AddTenantProductAsync(tenantB, $"Tenant B {Marker()}", sku);

        using (AmbientTenantContext.Enter(tenantA))
        {
            var result = await CatalogForCurrentTenantAsync(catalog => catalog.SearchAsync(sku, 10));
            var match = Assert.Single(result);
            Assert.Equal(productA.Id, match.Id);
            Assert.Null(await CatalogForCurrentTenantAsync(catalog => catalog.GetByIdAsync(productB.Id)));
        }

        using (AmbientTenantContext.Enter(tenantB))
        {
            var result = await CatalogForCurrentTenantAsync(catalog => catalog.SearchAsync(sku, 10));
            Assert.Equal(productB.Id, Assert.Single(result).Id);
            Assert.Null(await CatalogForCurrentTenantAsync(catalog => catalog.GetByIdAsync(productA.Id)));
        }
    }

    private async Task<IReadOnlyList<ProductCatalogSearchResult>> SearchAsync(string query) =>
        await CatalogAsync(catalog => catalog.SearchAsync(query, 10));

    private async Task<T> CatalogAsync<T>(Func<IProductCatalog, Task<T>> operation)
    {
        using var tenantScope = _factory.EnterDefaultTenant();
        await using var scope = _factory.Services.CreateAsyncScope();
        return await operation(scope.ServiceProvider.GetRequiredService<IProductCatalog>());
    }

    private async Task<T> CatalogForCurrentTenantAsync<T>(Func<IProductCatalog, Task<T>> operation)
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        return await operation(scope.ServiceProvider.GetRequiredService<IProductCatalog>());
    }

    private async Task<Product> AddProductAsync(
        string name,
        ProductStatus status,
        string sku,
        string? barcode = null,
        int? categoryId = null,
        string? description = null,
        decimal? standardCost = null,
        ProductPrice? price = null)
    {
        using var tenantScope = _factory.EnterDefaultTenant();
        await using var scope = _factory.Services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<ProductsDbContext>();
        var product = new Product
        {
            Name = name,
            Description = description,
            CategoryId = categoryId,
            Type = ProductType.Goods,
            Status = status,
            TaxCategoryId = 1001,
            Variants =
            [
                new ProductVariant
                {
                    Sku = sku,
                    Barcode = barcode,
                    StandardCost = standardCost,
                    Prices = price is null ? [] : [price],
                },
            ],
        };
        dbContext.Products.Add(product);
        await dbContext.SaveChangesAsync();
        return product;
    }

    private async Task<ProductCategory> AddCategoryAsync(string name)
    {
        using var tenantScope = _factory.EnterDefaultTenant();
        await using var scope = _factory.Services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<ProductsDbContext>();
        var category = new ProductCategory { Name = name };
        dbContext.ProductCategories.Add(category);
        await dbContext.SaveChangesAsync();
        return category;
    }

    private async Task<Product> AddTenantProductAsync(TenantId tenant, string name, string sku)
    {
        using var tenantScope = AmbientTenantContext.Enter(tenant);
        await using var scope = _factory.Services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<ProductsDbContext>();
        var taxCategory = new TaxCategory
        {
            TenantId = tenant.Value,
            Name = $"Tax {tenant.Value:N}",
            Kind = TaxCategoryKind.Standard,
            Rate = 0.25m,
        };
        dbContext.TaxCategories.Add(taxCategory);
        var product = new Product
        {
            TenantId = tenant.Value,
            Name = name,
            Type = ProductType.Goods,
            Status = ProductStatus.Active,
            TaxCategory = taxCategory,
            Variants = [new ProductVariant { TenantId = tenant.Value, Sku = sku }],
        };
        dbContext.Products.Add(product);
        await dbContext.SaveChangesAsync();
        return product;
    }

    private static string Marker() => Guid.NewGuid().ToString("N")[..12];

    private static string NewGtin13()
    {
        var digits = new int[13];
        for (var index = 0; index < 12; index++) digits[index] = Random.Shared.Next(10);
        var sum = 0;
        for (var index = 0; index < 12; index++) sum += digits[index] * ((12 - index) % 2 == 1 ? 3 : 1);
        digits[12] = (10 - sum % 10) % 10;
        return string.Concat(digits);
    }
}