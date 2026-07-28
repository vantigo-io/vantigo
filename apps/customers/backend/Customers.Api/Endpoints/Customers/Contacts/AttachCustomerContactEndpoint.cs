using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Domain.Contacts;
using Vantigo.Customers.Api.Endpoints.Customers.Contacts.Dtos;
using Vantigo.Customers.Api.Services;

namespace Vantigo.Customers.Api.Endpoints.Customers.Contacts;

/// <summary>
/// Associates an existing contact with a customer, giving it a role and optional
/// connection-specific contact details. A contact can be associated with a customer
/// at most once — repeated attempts return a conflict.
/// </summary>
internal static class AttachCustomerContactEndpoint
{
    internal static async Task<Results<Ok<CustomerContactResponse>, NotFound, ProblemHttpResult, ValidationProblem>> Handler(
        int id,
        Request request,
        AppDbContext dbContext,
        ICustomerTimelineRecorder timelineRecorder,
        CancellationToken cancellationToken)
    {
        var association = new CustomerContact
        {
            CustomerId = id,
            ContactId = request.ContactId,
        };

        if (!request.Connection.TryApplyTo(association, out var errors))
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid contact association");
        }

        // Serialize association creation with contact deletion on the contact row. This
        // ensures a delete either observes this association and records its removal, or
        // runs first and makes the attach a clean 404.
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var customer = await dbContext.Customers.FirstOrDefaultAsync(c => c.Id == id, cancellationToken);
        var contact = await dbContext.Contacts
            .FromSqlInterpolated($"SELECT * FROM contacts WHERE id = {request.ContactId} FOR UPDATE")
            .FirstOrDefaultAsync(cancellationToken);

        if (customer is null || contact is null)
        {
            return TypedResults.NotFound();
        }

        var alreadyAttached = await dbContext.CustomersContacts
            .AnyAsync(cc => cc.CustomerId == id && cc.ContactId == request.ContactId, cancellationToken);

        if (alreadyAttached)
        {
            return TypedResults.Problem(
                title: "Contact already associated",
                detail: $"Contact {request.ContactId} is already associated with customer {id}.",
                statusCode: StatusCodes.Status409Conflict);
        }

        association.Contact = contact;
        dbContext.CustomersContacts.Add(association);
        timelineRecorder.RecordContactAttached(customer, association);
        await dbContext.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);

        return TypedResults.Ok(CustomerContactResponse.FromDomain(association));
    }

    internal readonly record struct Request
    {
        /// <summary>The id of the existing contact to associate with the customer.</summary>
        public required int ContactId { get; init; }

        /// <summary>The role of the contact, such as "CEO".</summary>
        public required string Role { get; init; }

        /// <summary>A connection-specific phone number.</summary>
        public string? Phone { get; init; }

        /// <summary>A connection-specific email address.</summary>
        public string? Email { get; init; }

        internal CustomerContactRequest Connection => new()
        {
            Role = Role,
            Phone = Phone,
            Email = Email,
        };
    }
}
