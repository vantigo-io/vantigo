using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;

namespace Vantigo.Products.Database.DevelopmentSeed;

/// <summary>
/// Creates a small, repeatable local dataset. This is deliberately kept outside the
/// migration model: it is useful for development, but is not application data that
/// should be deployed to another environment.
/// </summary>
internal static class DevelopmentDataSeeder
{
    private static readonly DateTimeOffset CampaignStart = new(2026, 8, 1, 0, 0, 0, TimeSpan.Zero);
    private static readonly DateTimeOffset CampaignEnd = new(2027, 8, 1, 0, 0, 0, TimeSpan.Zero);

    internal static async Task SeedProductsAsync(this IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
        await SeedProductsAsync(scope.ServiceProvider.GetRequiredService<ProductsDbContext>(), cancellationToken);
    }

    private static async Task SeedProductsAsync(
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var categoryIdsByName = await SeedCategoriesAsync(dbContext, cancellationToken);

        foreach (var seed in CreateProductSeeds(categoryIdsByName))
        {
            var product = await dbContext.Products
                .Include(item => item.Prices)
                .FirstOrDefaultAsync(item => item.Sku == seed.Sku, cancellationToken);

            if (product is not null)
            {
                continue;
            }

            dbContext.Products.Add(seed);
        }

        await dbContext.SaveChangesAsync(cancellationToken);
    }

    private static async Task<IReadOnlyDictionary<string, int>> SeedCategoriesAsync(
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        // (name, parent name) pairs; parents must precede their children.
        (string Name, string? ParentName)[] seeds =
        [
            ("Furniture", null),
            ("Desks", "Furniture"),
            ("Lighting", "Furniture"),
            ("Services", null),
        ];

        var idsByName = new Dictionary<string, int>();
        foreach (var (name, parentName) in seeds)
        {
            int? parentId = parentName is null ? null : idsByName[parentName];
            var category = await dbContext.ProductCategories.FirstOrDefaultAsync(
                c => c.Name == name && c.ParentId == parentId,
                cancellationToken);

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
        IReadOnlyDictionary<string, int> categoryIdsByName) =>
    [
        new Product
        {
            Name = "Aurora Desk Lamp",
            Sku = "AUR-001",
            Type = ProductType.Goods,
            Status = ProductStatus.Active,
            Unit = "pcs",
            StandardCost = 240m,
            VatRate = 0.25m,
            Description = "A warm-white LED desk lamp with a weighted base and stepless dimming.",
            CategoryId = categoryIdsByName["Lighting"],
            Barcode = "7350053850019",
            WeightKg = 1.2m,
            LengthCm = 18m,
            WidthCm = 18m,
            HeightCm = 45m,
            Prices =
            [
                new ProductPrice { Currency = "NOK", Amount = 599m },
                new ProductPrice { Currency = "SEK", Amount = 649m },
                // A campaign price temporarily undercuts the open-ended base price.
                new ProductPrice
                {
                    Currency = "NOK",
                    Amount = 499m,
                    ValidFrom = CampaignStart,
                    ValidTo = CampaignEnd,
                },
            ],
        },
        new Product
        {
            Name = "Aurora Desk Lamp 10-pack",
            Sku = "AUR-001-10PK",
            Type = ProductType.Goods,
            Status = ProductStatus.Active,
            Unit = "pcs",
            StandardCost = 2200m,
            VatRate = 0.25m,
            Description = "Ten Aurora desk lamps in a single carton for office rollouts.",
            CategoryId = categoryIdsByName["Lighting"],
            Barcode = "7350053850026",
            WeightKg = 13.5m,
            LengthCm = 60m,
            WidthCm = 40m,
            HeightCm = 50m,
            Prices = [new ProductPrice { Currency = "NOK", Amount = 5290m }],
        },
        new Product
        {
            Name = "Fjord Standing Desk",
            Sku = "FJD-100",
            Type = ProductType.Goods,
            Status = ProductStatus.Active,
            Unit = "pcs",
            StandardCost = 3100m,
            VatRate = 0.25m,
            Description = "An electric sit-stand desk with an oak veneer top and dual motors.",
            CategoryId = categoryIdsByName["Desks"],
            Barcode = "7350053850033",
            WeightKg = 38m,
            LengthCm = 160m,
            WidthCm = 80m,
            HeightCm = 12m,
            Prices = [new ProductPrice { Currency = "NOK", Amount = 7990m }],
        },
        new Product
        {
            Name = "On-site Installation",
            Sku = "SRV-INSTALL",
            Type = ProductType.Service,
            Status = ProductStatus.Active,
            Unit = "hour",
            StandardCost = 650m,
            VatRate = 0.25m,
            Description = "Assembly and installation of purchased furniture at the customer site.",
            CategoryId = categoryIdsByName["Services"],
            Prices = [new ProductPrice { Currency = "NOK", Amount = 1290m }],
        },
        new Product
        {
            Name = "Workspace Consultation",
            Sku = "SRV-CONSULT",
            Type = ProductType.Service,
            Status = ProductStatus.Draft,
            Unit = "hour",
            VatRate = 0.25m,
            CategoryId = categoryIdsByName["Services"],
            Prices = [new ProductPrice { Currency = "NOK", Amount = 1590m }],
        },
        new Product
        {
            Name = "Meadow Office Chair (2025)",
            Sku = "MDW-2025",
            Type = ProductType.Goods,
            Status = ProductStatus.Discontinued,
            Unit = "pcs",
            StandardCost = 900m,
            VatRate = 0.25m,
            CategoryId = categoryIdsByName["Furniture"],
            Barcode = "7350053850040",
            WeightKg = 14m,
            Prices = [new ProductPrice { Currency = "NOK", Amount = 2490m }],
        },
    ];

}