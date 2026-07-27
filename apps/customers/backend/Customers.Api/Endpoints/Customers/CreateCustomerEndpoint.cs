using Microsoft.AspNetCore.Http.HttpResults;

using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Domain.Customers;
using Vantigo.Customers.Api.Domain.Customers.Common;
using Vantigo.Customers.Api.Domain.Customers.ValueObjects;
using Vantigo.Customers.Api.Endpoints.Customers.Dtos;

namespace Vantigo.Customers.Api.Endpoints.Customers;

/// <summary>
/// Creates a new customer. A customer only requires a friendly name at creation time.
/// The legal identity is optional and can be attached later, for instance after it has
/// been verified against a public registry.
/// </summary>
internal static class CreateCustomerEndpoint
{
    internal static async Task<Results<CreatedAtRoute<Response>, ValidationProblem>> Handler(
        Request request,
        AppDbContext dbContext,
        CancellationToken cancellationToken)
    {
        // All validation errors are collected up front and keyed by the JSON path of
        // the offending request field, so API clients can map them directly onto
        // form fields.
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

        var customer = new Customer
        {
            Name = name,
            Identity = customerIdentity,
        };

        dbContext.Customers.Add(customer);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.CreatedAtRoute(
            new Response { Id = customer.Id },
            CustomersEndpoints.GetCustomerRouteName,
            new { id = customer.Id });
    }

    internal readonly record struct Request
    {
        public required string Name { get; init; }
        public LegalIdentityRequest? Identity { get; init; }
    }

    internal readonly record struct Response
    {
        public required int Id { get; init; }
    }
}