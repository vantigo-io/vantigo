using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Accounts;
using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Domain.Products;
using Vantigo.Products.Api.Endpoints.Auth;

namespace Vantigo.Products.Api.Database.DevelopmentSeed;

/// <summary>
/// Creates a small, repeatable local dataset. This is deliberately kept outside the
/// migration model: it is useful for development, but is not application data that
/// should be deployed to another environment.
/// </summary>
internal static class DevelopmentDataSeeder
{
    private static readonly Guid DevelopmentOwnerId = new("7f4d1e5b-8a62-4b7e-9c13-2d5f6a708194");
    private static readonly Guid DevelopmentOwnerRoleId = new("8e5c2f6c-9b73-4c8f-ad24-3e607b8192a5");
    private static readonly Guid DevelopmentUserRoleId = new("9f6d307d-ac84-4d90-be35-4f718c92a3b6");
    private static readonly DateTimeOffset DevelopmentBootstrapCompletedAt =
        new(2026, 8, 4, 0, 0, 0, TimeSpan.Zero);

    private static readonly DateTimeOffset CampaignStart = new(2026, 8, 1, 0, 0, 0, TimeSpan.Zero);
    private static readonly DateTimeOffset CampaignEnd = new(2027, 8, 1, 0, 0, 0, TimeSpan.Zero);

    internal static async Task SeedDevelopmentDataAsync(this WebApplication app)
    {
        await using var scope = app.Services.CreateAsyncScope();

        var accountsDbContext = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        await SeedDevelopmentOwnerAsync(
            accountsDbContext,
            scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>(),
            scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>(),
            app.Configuration,
            app.Lifetime.ApplicationStopping);

        await SeedProductsAsync(
            scope.ServiceProvider.GetRequiredService<ProductsDbContext>(),
            app.Lifetime.ApplicationStopping);
    }

    private static async Task SeedDevelopmentOwnerAsync(
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        RoleManager<IdentityRole<Guid>> roleManager,
        IConfiguration configuration,
        CancellationToken cancellationToken)
    {
        var adminConfiguration = configuration.GetSection("Development:Seed:Admin");
        var email = adminConfiguration["Email"]?.Trim();
        var displayName = adminConfiguration["DisplayName"]?.Trim();
        var password = adminConfiguration["Password"];

        if (string.IsNullOrWhiteSpace(email) ||
            string.IsNullOrWhiteSpace(displayName) ||
            string.IsNullOrWhiteSpace(password))
        {
            throw new InvalidOperationException(
                "Development:Seed:Admin requires Email, DisplayName, and Password in Development configuration.");
        }

        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable,
            cancellationToken);

        await EnsureRoleAsync(roleManager, AuthRoles.Owner, cancellationToken);
        await EnsureRoleAsync(roleManager, AuthRoles.User, cancellationToken);

        var user = await userManager.FindByEmailAsync(email);
        if (user is null)
        {
            user = new ApplicationUser
            {
                Id = DevelopmentOwnerId,
                UserName = email,
                Email = email,
                EmailConfirmed = true,
                DisplayName = displayName,
            };

            var createResult = await userManager.CreateAsync(user, password);
            EnsureIdentitySuccess(createResult, "The development Owner account could not be created.");
        }

        await EnsureUserRoleAsync(userManager, user, AuthRoles.Owner);
        await EnsureUserRoleAsync(userManager, user, AuthRoles.User);

        // Keep the one-time bootstrap endpoint unavailable after this account has
        // been created, matching the marker written by the bootstrap endpoint.
        if (!await dbContext.BootstrapStates.AnyAsync(state => state.Id == 1, cancellationToken))
        {
            dbContext.BootstrapStates.Add(new BootstrapState
            {
                Id = 1,
                CompletedAt = DevelopmentBootstrapCompletedAt,
            });
            await dbContext.SaveChangesAsync(cancellationToken);
        }

        await transaction.CommitAsync(cancellationToken);
    }

    private static async Task EnsureRoleAsync(
        RoleManager<IdentityRole<Guid>> roleManager,
        string roleName,
        CancellationToken cancellationToken)
    {
        if (await roleManager.FindByNameAsync(roleName) is not null)
        {
            return;
        }

        var roleId = roleName switch
        {
            AuthRoles.Owner => DevelopmentOwnerRoleId,
            AuthRoles.User => DevelopmentUserRoleId,
            _ => throw new InvalidOperationException($"Unknown development role '{roleName}'."),
        };
        var result = await roleManager.CreateAsync(new IdentityRole<Guid>
        {
            Id = roleId,
            Name = roleName,
            NormalizedName = roleName.ToUpperInvariant(),
        });
        EnsureIdentitySuccess(result, $"The development role '{roleName}' could not be created.");
    }

    private static async Task EnsureUserRoleAsync(
        UserManager<ApplicationUser> userManager,
        ApplicationUser user,
        string roleName)
    {
        if (await userManager.IsInRoleAsync(user, roleName))
        {
            return;
        }

        var result = await userManager.AddToRoleAsync(user, roleName);
        EnsureIdentitySuccess(result, $"The development account could not be assigned the '{roleName}' role.");
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

    private static void EnsureIdentitySuccess(IdentityResult result, string message)
    {
        if (result.Succeeded)
        {
            return;
        }

        var details = string.Join(" ", result.Errors.Select(error => error.Description));
        throw new InvalidOperationException($"{message} {details}".Trim());
    }
}