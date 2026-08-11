using Vantigo.Contracts.Identity;
using Vantigo.Customers.Endpoints.Customers;
using Vantigo.Customers.Endpoints.Customers.Contacts;

namespace Vantigo.Customers.Endpoints;

internal static class CustomersEndpoints
{
    internal const string GetCustomerRouteName = "GetCustomer";

    internal static IEndpointRouteBuilder MapCustomersEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("")
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

        group.MapGet("/{id:int}/timeline", TimelineEndpoints.List)
            .WithSummary("List a customer's timeline")
            .WithDescription("Filters: provenance, repeated eventType, occurredFrom, and occurredTo. Cursors are scoped to these filters.")
            .Produces<TimelineListResponse>(StatusCodes.Status200OK)
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .Produces(StatusCodes.Status404NotFound);

        group.MapPost("/{id:int}/timeline", TimelineEndpoints.Create)

            .WithSummary("Create a manual customer timeline entry")
            .Produces<TimelineResponse>(StatusCodes.Status201Created)
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .Produces(StatusCodes.Status404NotFound);

        group.MapGet("/{id:int}/timeline/{entryId:int}", TimelineEndpoints.Get)
            .WithSummary("Get a customer timeline entry")
            .Produces<TimelineResponse>(StatusCodes.Status200OK)
            .Produces(StatusCodes.Status404NotFound);

        group.MapPut("/{id:int}/timeline/{entryId:int}", TimelineEndpoints.Update)

            .WithSummary("Update a manual customer timeline entry")
            .Produces<TimelineResponse>(StatusCodes.Status200OK)
            .ProducesProblem(StatusCodes.Status400BadRequest)
            .Produces(StatusCodes.Status404NotFound)
            .ProducesProblem(StatusCodes.Status409Conflict);

        group.MapDelete("/{id:int}/timeline/{entryId:int}", TimelineEndpoints.Delete)

            .WithSummary("Delete a manual customer timeline entry")
            .Produces(StatusCodes.Status204NoContent)
            .Produces(StatusCodes.Status404NotFound)
            .ProducesProblem(StatusCodes.Status409Conflict);

        group.MapGet("/{id:int}/timeline/{entryId:int}/revisions", TimelineEndpoints.Revisions)
            .WithSummary("List timeline entry revisions")
            .Produces<TimelineRevisionListResponse>(StatusCodes.Status200OK)
            .Produces(StatusCodes.Status404NotFound);

        return app;
    }
}