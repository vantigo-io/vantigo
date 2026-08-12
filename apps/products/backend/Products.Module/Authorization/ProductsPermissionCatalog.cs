using Vantigo.Contracts.Authorization;

namespace Vantigo.Products.Authorization;

public sealed class ProductsPermissionCatalogContributor : IPermissionCatalogContributor
{
    public void Contribute(PermissionCatalogBuilder catalog) => catalog.AddRange([
        Permission("products:products-view", "View products", "View products and their details.", "Products"),
        Permission("products:products-manage", "Manage products", "Create, update, and archive products.", "Products"),
        Permission("products:variants-view", "View variants", "View product variants.", "Variants"),
        Permission("products:variants-manage", "Manage variants", "Create, update, and delete product variants.", "Variants"),
        Permission("products:pricing-view", "View pricing", "View product variant prices.", "Pricing"),
        Permission("products:pricing-manage", "Manage pricing", "Create, update, and delete product variant prices.", "Pricing"),
        Permission("products:categories-view", "View categories", "View product categories.", "Categories"),
        Permission("products:categories-manage", "Manage categories", "Create, update, and delete product categories.", "Categories"),
        Permission("products:tax-categories-view", "View tax categories", "View product tax categories.", "Tax categories"),
        Permission("products:tax-categories-manage", "Manage tax categories", "Create, update, and delete product tax categories.", "Tax categories"),
    ]);

    private static PermissionDescriptor Permission(string key, string displayName, string description, string category) =>
        new(key, displayName, description, "products", category, Delegable: true);
}