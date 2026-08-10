namespace Vantigo.Products.Endpoints;

public static class ProductsModuleEndpointExtensions
{
    public static IEndpointRouteBuilder MapProductsModule(this IEndpointRouteBuilder endpoints)
    {
        endpoints.MapVersionedBusinessEndpoints();
        return endpoints;
    }
}