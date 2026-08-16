using System.Security.Claims;

using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Authorization;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Customers.Common;
using Vantigo.Customers.Domain.Customers.ValueObjects;
using Vantigo.Customers.Endpoints.Customers.Dtos;
using Vantigo.Customers.Services;

namespace Vantigo.Customers.Endpoints.Customers;

/// <summary>
/// Updates an existing customer. The request carries the desired final state of the
/// customer's editable fields: the friendly name is required, while the legal identity
/// is replaced when given and removed when omitted or null.
/// </summary>
internal static class UpdateCustomerEndpoint
{
    internal static async Task<IResult> Handler(
        int id,
        Request request,
        ClaimsPrincipal principal,
        IAuthorizationService authorization,
        CustomersDbContext dbContext,
        ICustomerTimelineRecorder timelineRecorder,
        CancellationToken cancellationToken)
    {
        var errors = new Dictionary<string, string[]>();

        if (request.Identity is not null && !await CustomerAuthorization.HasPermissionAsync(
                authorization, principal, CustomerPermissions.LegalIdentityManage))
        {
            return TypedResults.Forbid();
        }

        if (!FriendlyName.TryCreate(request.Name, out var name, out var nameError))
        {
            errors["name"] = [nameError!];
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

        LegalIdentity? customerIdentity = customer.Identity;
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
                return TypedResults.ValidationProblem(errors, title: "Invalid customer");
            }
        }

        var before = CustomerSnapshot.From(customer);
        var changed = customer.Name != name || customer.Identity != customerIdentity;
        customer.Name = name;
        customer.Identity = customerIdentity;
        if (changed)
        {
            timelineRecorder.RecordCustomerUpdated(customer, before);
        }
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(new SafeCustomerResponse
        {
            Id = customer.Id,
            CustomerNumber = customer.CustomerNumber,
            Name = customer.Name,
            TimelineSummary = await SafeCustomerProjection.TimelineSummaryAsync(dbContext, customer.Id, cancellationToken),
        });
    }

    internal readonly record struct Request
    {
        public required string Name { get; init; }
        public LegalIdentityRequest? Identity { get; init; }
    }
}