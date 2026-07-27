using Vantigo.Customers.Api.Endpoints.Customers;
using Vantigo.Customers.Api.Endpoints.Customers.Contacts;

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

        group.MapGet("/{id:int}/contacts", GetCustomerContactsEndpoint.Handler)
            .WithSummary("List the contacts associated with a customer");

        group.MapPost("/{id:int}/contacts", AttachCustomerContactEndpoint.Handler)
            .WithSummary("Associate a contact with a customer");

        group.MapPut("/{id:int}/contacts/{contactId:int}", UpdateCustomerContactEndpoint.Handler)
            .WithSummary("Update a customer's contact association");

        group.MapDelete("/{id:int}/contacts/{contactId:int}", DetachCustomerContactEndpoint.Handler)
            .WithSummary("Remove a contact association from a customer");

        return app;
    }
}