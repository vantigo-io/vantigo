using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Customers.Authorization;
using Vantigo.Customers.Endpoints.Contacts;

namespace Vantigo.Customers.Endpoints;

internal static class ContactsEndpoints
{
    internal const string GetContactRouteName = "GetContact";

    internal static IEndpointRouteBuilder MapContactsEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/contacts")
            .WithTags("Contacts");

        group.MapGet("/", GetContactsEndpoint.Handler)
            .WithSummary("List all contacts")
            .RequirePermission(CustomerPermissions.ContactsView)
            .RequirePermission(CustomerPermissions.AssociationsView);

        group.MapPost("/", CreateContactEndpoint.Handler)
            .WithSummary("Create a new contact")
            .RequirePermission(CustomerPermissions.ContactsManage)
            .RequirePermission(CustomerPermissions.ContactsView);

        group.MapGet("/{id:int}", GetContactEndpoint.Handler)
            .WithName(GetContactRouteName)
            .WithSummary("Get a contact by id")
            .RequirePermission(CustomerPermissions.ContactsView);

        group.MapPut("/{id:int}", UpdateContactEndpoint.Handler)
            .WithSummary("Update a contact")
            .RequirePermission(CustomerPermissions.ContactsManage)
            .RequirePermission(CustomerPermissions.ContactsView);

        group.MapGet("/{id:int}/customers", GetContactCustomersEndpoint.Handler)
            .WithSummary("List the customers a contact is associated with")
            .RequirePermission(CustomerPermissions.AssociationsView)
            .RequirePermission(CustomerPermissions.ContactsView);

        group.MapDelete("/{id:int}", DeleteContactEndpoint.Handler)
            .WithSummary("Delete a contact")
            .RequirePermission(CustomerPermissions.ContactsManage)
            .RequirePermission(CustomerPermissions.AssociationsManage);

        return app;
    }
}