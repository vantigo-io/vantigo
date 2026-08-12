using System.Security.Claims;

using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Http.HttpResults;

using Vantigo.Customers.Authorization;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Customers.Domain.Customers.Common;
using Vantigo.Customers.Domain.Customers.ValueObjects;
using Vantigo.Customers.Endpoints.Customers.Dtos;
using Vantigo.Customers.Services;

namespace Vantigo.Customers.Endpoints.Customers;

/// <summary>
/// Creates a new customer. A customer only requires a friendly name at creation time.
/// The legal identity is optional and can be attached later, for instance after it has
/// been verified against a public registry.
/// </summary>
internal static class CreateCustomerEndpoint
{
    internal static async Task<IResult> Handler(
        Request request,
        ClaimsPrincipal principal,
        IAuthorizationService authorization,
        CustomersDbContext dbContext,
        ICustomerTimelineRecorder timelineRecorder,
        CancellationToken cancellationToken)
    {
        // All validation errors are collected up front and keyed by the JSON path of
        // the offending request field, so API clients can map them directly onto
        // form fields.
        var errors = new Dictionary<string, string[]>();

        if (request.Identity is not null && !await CustomerAuthorization.HasPermissionAsync(
                authorization, principal, CustomerPermissions.LegalIdentityManage))
        {
            return TypedResults.Forbid();
        }

        LegalIdentity? customerIdentity = null;
        if (request.Identity is { } identity)
        {
            if (LegalIdentity.TryCreate(identity.Country, identity.Type, identity.Id, identity.Name, identity.Source,
                    out var parsedIdentity, out var identityErrors))
            {
                customerIdentity = parsedIdentity;
            }
            else
            {
                foreach (var (field, fieldErrors) in identityErrors)
                {
                    errors[$"identity.{field}"] = fieldErrors;
                }
            }
        }

        if (!FriendlyName.TryCreate(request.Name, out var name, out var nameError))
        {
            errors["name"] = [nameError!];
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

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        dbContext.Customers.Add(customer);
        // The customer identity is database-generated, so stage the event after the
        // insert has been flushed, but keep both operations in the same transaction.
        await dbContext.SaveChangesAsync(cancellationToken);
        timelineRecorder.RecordCustomerCreated(customer);
        await dbContext.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);

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