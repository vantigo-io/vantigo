using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Endpoints.Customers.Contacts.Dtos;
using Vantigo.Customers.Services;

namespace Vantigo.Customers.Endpoints.Customers.Contacts;

/// <summary>
/// Updates the role and connection-specific contact details of an existing
/// customer-contact association.
/// </summary>
internal static class UpdateCustomerContactEndpoint
{
    internal static async Task<Results<Ok<CustomerContactResponse>, NotFound, ValidationProblem>> Handler(
        int id,
        int contactId,
        CustomerContactRequest request,
        CustomersDbContext dbContext,
        ICustomerTimelineRecorder timelineRecorder,
        CancellationToken cancellationToken)
    {
        var association = await dbContext.CustomersContacts
            .Include(cc => cc.Contact)
            .Include(cc => cc.Customer)
            .FirstOrDefaultAsync(cc => cc.CustomerId == id && cc.ContactId == contactId, cancellationToken);

        if (association is null)
        {
            return TypedResults.NotFound();
        }

        var previousRole = association.Role;
        var previousPhone = association.Phone;
        var previousEmail = association.Email;

        if (!request.TryApplyTo(association, out var errors))
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid contact association");
        }

        var changed = association.Role != previousRole ||
                      association.Phone != previousPhone ||
                      association.Email != previousEmail;
        if (changed)
        {
            timelineRecorder.RecordContactRelationshipUpdated(association.Customer, association);
        }
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(CustomerContactResponse.FromDomain(association));
    }
}