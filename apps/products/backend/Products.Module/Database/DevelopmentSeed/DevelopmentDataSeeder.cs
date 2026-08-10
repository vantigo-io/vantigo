using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Database.DevelopmentSeed;

/// <summary>Creates a small, repeatable local dataset outside the migration model.</summary>
internal static class DevelopmentDataSeeder
{
    private static readonly DateTimeOffset CampaignStart = new(2026, 8, 1, 0, 0, 0, TimeSpan.Zero);
    private static readonly DateTimeOffset CampaignEnd = new(2027, 8, 1, 0, 0, 0, TimeSpan.Zero);

    internal static async Task SeedProductsAsync(this IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
        await SeedProductsAsync(scope.ServiceProvider.GetRequiredService<ProductsDbContext>(), cancellationToken);
    }

    private static async Task SeedProductsAsync(ProductsDbContext dbContext, CancellationToken cancellationToken)
    {
        var categoryIdsByName = await SeedCategoriesAsync(dbContext, cancellationToken);
        var taxCategoryIdsByName = await SeedTaxCategoriesAsync(dbContext, cancellationToken);
        foreach (var seed in CreateProductSeeds(categoryIdsByName, taxCategoryIdsByName))
        {
            if (!await dbContext.ProductVariants.AnyAsync(variant => variant.Sku == seed.Variants[0].Sku, cancellationToken))
            {
                dbContext.Products.Add(seed);
            }
        }

        await dbContext.SaveChangesAsync(cancellationToken);
    }

    private static async Task<IReadOnlyDictionary<string, int>> SeedTaxCategoriesAsync(
        ProductsDbContext dbContext, CancellationToken cancellationToken)
    {
        (string Name, TaxCategoryKind Kind, decimal Rate)[] seeds =
        [
            ("Standard 25%", TaxCategoryKind.Standard, 0.25m),
            ("Reduced/Food 15%", TaxCategoryKind.Reduced, 0.15m),
            ("Zero 0%", TaxCategoryKind.Zero, 0m),
            ("Exempt 0%", TaxCategoryKind.Exempt, 0m),
        ];
        var idsByName = new Dictionary<string, int>();
        foreach (var (name, kind, rate) in seeds)
        {
            var category = await dbContext.TaxCategories.FirstOrDefaultAsync(c => c.Name == name, cancellationToken);
            if (category is null)
            {
                category = new TaxCategory { Name = name, Kind = kind, Rate = rate };
                dbContext.TaxCategories.Add(category);
                await dbContext.SaveChangesAsync(cancellationToken);
            }

            idsByName[name] = category.Id;
        }

        return idsByName;
    }

    private static async Task<IReadOnlyDictionary<string, int>> SeedCategoriesAsync(
        ProductsDbContext dbContext, CancellationToken cancellationToken)
    {
        (string Name, string? ParentName)[] seeds =
        [
            ("Furniture", null), ("Desks", "Furniture"), ("Lighting", "Furniture"), ("Services", null),
        ];
        var idsByName = new Dictionary<string, int>();
        foreach (var (name, parentName) in seeds)
        {
            int? parentId = parentName is null ? null : idsByName[parentName];
            var category = await dbContext.ProductCategories.FirstOrDefaultAsync(
                c => c.Name == name && c.ParentId == parentId, cancellationToken);
            if (category is null)
            {
                category = new ProductCategory { Name = name, ParentId = parentId };
                dbContext.ProductCategories.Add(category);
                await dbContext.SaveChangesAsync(cancellationToken);
            }

            idsByName[name] = category.Id;
        }

        return idsByName;
    }

    private static IReadOnlyList<Product> CreateProductSeeds(
        IReadOnlyDictionary<string, int> categoryIdsByName,
        IReadOnlyDictionary<string, int> taxCategoryIdsByName) =>
    [
        new Product
        {
            Name = "Aurora Desk Lamp", Type = ProductType.Goods, Status = ProductStatus.Active,
            TaxCategoryId = taxCategoryIdsByName["Standard 25%"], Description = "A warm-white LED desk lamp with a weighted base and stepless dimming.",
            CategoryId = categoryIdsByName["Lighting"],
            Variants =
            [
                new ProductVariant
                {
                    Sku = "AUR-001", Barcode = "7350053850019", StandardCost = 240m,
                    WeightKg = 1.2m, LengthCm = 18m, WidthCm = 18m, HeightCm = 45m,
                    Prices =
                    [
                        new ProductPrice { Currency = "NOK", Amount = 599m },
                        new ProductPrice { Currency = "SEK", Amount = 649m },
                        new ProductPrice { Currency = "NOK", Amount = 499m, ValidFrom = CampaignStart, ValidTo = CampaignEnd },
                    ],
                },
            ],
        },
        new Product
        {
            Name = "Aurora Desk Lamp 10-pack", Type = ProductType.Goods, Status = ProductStatus.Active,
            TaxCategoryId = taxCategoryIdsByName["Standard 25%"], Description = "Ten Aurora desk lamps in a single carton for office rollouts.",
            CategoryId = categoryIdsByName["Lighting"],
            Variants = [new ProductVariant { Sku = "AUR-001-10PK", Barcode = "7350053850026", StandardCost = 2200m, WeightKg = 13.5m, LengthCm = 60m, WidthCm = 40m, HeightCm = 50m, Prices = [new ProductPrice { Currency = "NOK", Amount = 5290m }] }],
        },
        new Product
        {
            Name = "Fjord Standing Desk", Type = ProductType.Goods, Status = ProductStatus.Active,
            TaxCategoryId = taxCategoryIdsByName["Standard 25%"], Description = "An electric sit-stand desk with an oak veneer top and dual motors.",
            CategoryId = categoryIdsByName["Desks"],
            Variants =
            [
                new ProductVariant { Sku = "FJD-100-BLACK", StandardCost = 3100m, WeightKg = 38m, LengthCm = 160m, WidthCm = 80m, HeightCm = 12m, OptionValues = new() { ["Color"] = "Black" }, Prices = [new ProductPrice { Currency = "NOK", Amount = 7990m }] },
                new ProductVariant { Sku = "FJD-100-WHITE", StandardCost = 3100m, WeightKg = 38m, LengthCm = 160m, WidthCm = 80m, HeightCm = 12m, OptionValues = new() { ["Color"] = "White" }, Prices = [new ProductPrice { Currency = "NOK", Amount = 7990m }] },
            ],
        },
        new Product
        {
            Name = "On-site Installation", Type = ProductType.Service, Status = ProductStatus.Active,
            TaxCategoryId = taxCategoryIdsByName["Standard 25%"], Description = "Assembly and installation of purchased furniture at the customer site.",
            CategoryId = categoryIdsByName["Services"],
            Variants = [new ProductVariant { Sku = "SRV-INSTALL", Unit = "hour", StandardCost = 650m, Prices = [new ProductPrice { Currency = "NOK", Amount = 1290m }] }],
        },
        new Product
        {
            Name = "Workspace Consultation", Type = ProductType.Service, Status = ProductStatus.Draft,
            TaxCategoryId = taxCategoryIdsByName["Standard 25%"], CategoryId = categoryIdsByName["Services"],
            Variants = [new ProductVariant { Sku = "SRV-CONSULT", Unit = "hour", Prices = [new ProductPrice { Currency = "NOK", Amount = 1590m }] }],
        },
        new Product
        {
            Name = "Meadow Office Chair (2025)", Type = ProductType.Goods, Status = ProductStatus.Discontinued,
            TaxCategoryId = taxCategoryIdsByName["Standard 25%"], CategoryId = categoryIdsByName["Furniture"],
            Variants = [new ProductVariant { Sku = "MDW-2025", Barcode = "7350053850040", StandardCost = 900m, WeightKg = 14m, Prices = [new ProductPrice { Currency = "NOK", Amount = 2490m }] }],
        },
    ];
}