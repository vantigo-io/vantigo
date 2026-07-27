using Vantigo.Customers.Api.Endpoints.Contacts;

namespace Vantigo.Customers.Api.Endpoints;

internal static class ContactsEndpoints
{
    internal const string GetContactRouteName = "GetContact";

    internal static IEndpointRouteBuilder MapContactsEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/contacts")
            .WithTags("Contacts");

        group.MapGet("/", GetContactsEndpoint.Handler)
            .WithSummary("List all contacts");

        group.MapPost("/", CreateContactEndpoint.Handler)
            .WithSummary("Create a new contact");

        group.MapGet("/{id:int}", GetContactEndpoint.Handler)
            .WithName(GetContactRouteName)
            .WithSummary("Get a contact by id");

        group.MapPut("/{id:int}", UpdateContactEndpoint.Handler)
            .WithSummary("Update a contact");

        group.MapGet("/{id:int}/customers", GetContactCustomersEndpoint.Handler)
            .WithSummary("List the customers a contact is associated with");

        group.MapDelete("/{id:int}", DeleteContactEndpoint.Handler)
            .WithSummary("Delete a contact");

        return app;
    }
}