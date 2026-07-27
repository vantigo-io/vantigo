using Vantigo.Customers.Api.Endpoints.Customers;

namespace Vantigo.Customers.Api.Endpoints;

internal static class CustomersEndpoints
{
    internal const string GetCustomerRouteName = "GetCustomer";

    internal static IEndpointRouteBuilder MapCustomersEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/customers")
            .WithTags("Customers");

        group.MapGet("/", GetCustomersEndpoint.Handler)
            .WithSummary("List all customers");

        group.MapPost("/", CreateCustomerEndpoint.Handler)
            .WithSummary("Create a new customer");

        group.MapGet("/{id:int}", GetCustomerEndpoint.Handler)
            .WithName(GetCustomerRouteName)
            .WithSummary("Get a customer by id");

        group.MapPut("/{id:int}", UpdateCustomerEndpoint.Handler)
            .WithSummary("Update a customer");

        return app;
    }
}
