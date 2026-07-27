using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Domain.Customers.Common;
using Vantigo.Customers.Api.Domain.Customers.ValueObjects;
using Vantigo.Customers.Api.Endpoints.Customers.Dtos;

namespace Vantigo.Customers.Api.Endpoints.Customers;

/// <summary>
/// Updates an existing customer. The request carries the desired final state of the
/// customer's editable fields: the friendly name is required, while the legal identity
/// is replaced when given and removed when omitted or null.
/// </summary>
internal static class UpdateCustomerEndpoint
{
    internal static async Task<Results<Ok<CustomerResponse>, NotFound, ValidationProblem>> Handler(
        int id,
        Request request,
        AppDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = new Dictionary<string, string[]>();

        if (!FriendlyName.TryCreate(request.Name, out var name, out var nameError))
        {
            errors["name"] = [nameError!];
        }

        LegalIdentity? customerIdentity = null;

        if (request.Identity is { } identity)
        {
            if (LegalIdentity.TryCreate(
                    identity.Country,
                    identity.Type,
                    identity.Id,
                    identity.Name,
                    identity.Source,
                    out var legalIdentity,
                    out var identityErrors))
            {
                customerIdentity = legalIdentity;
            }
            else
            {
                foreach (var (field, fieldErrors) in identityErrors)
                {
                    errors[$"identity.{field}"] = fieldErrors;
                }
            }
        }

        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid customer");
        }

        var customer = await dbContext.Customers
            .FirstOrDefaultAsync(c => c.Id == id, cancellationToken);

        if (customer is null)
        {
            return TypedResults.NotFound();
        }

        customer.Name = name;
        customer.Identity = customerIdentity;
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(CustomerResponse.FromDomain(customer));
    }

    internal readonly record struct Request
    {
        public required string Name { get; init; }
        public LegalIdentityRequest? Identity { get; init; }
    }
}