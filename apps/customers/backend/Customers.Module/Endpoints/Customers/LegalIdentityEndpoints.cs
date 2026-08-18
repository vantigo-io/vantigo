using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Customers.Domain.Customers.ValueObjects;
using Vantigo.Customers.Endpoints.Customers.Dtos;
using Vantigo.Customers.Services;

namespace Vantigo.Customers.Endpoints.Customers;

internal static class LegalIdentityEndpoints
{
    internal static async Task<IResult> Get(
        int id,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var customer = await dbContext.Customers.AsNoTracking()
            .SingleOrDefaultAsync(customer => customer.Id == id, cancellationToken);

        if (customer is null)
        {
            return TypedResults.NotFound();
        }

        return customer.Identity is { } identity
            ? TypedResults.Ok(LegalIdentityResponse.FromDomain(identity))
            : TypedResults.NoContent();
    }

    internal static async Task<IResult> Upsert(
        int id,
        LegalIdentityRequest request,
        CustomersDbContext dbContext,
        ICustomerTimelineRecorder timelineRecorder,
        CancellationToken cancellationToken)
    {
        if (!TryParse(request, out var identity, out var errors))
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid legal identity");
        }

        var customer = await dbContext.Customers.FirstOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (customer is null)
        {
            return TypedResults.NotFound();
        }

        var before = CustomerSnapshot.From(customer);
        customer.Identity = identity;
        if (before.Identity != CustomerSnapshot.From(customer).Identity)
        {
            timelineRecorder.RecordCustomerUpdated(customer, before);
            customer.UpdatedAt = DateTimeOffset.UtcNow;
        }

        await dbContext.SaveChangesAsync(cancellationToken);
        return TypedResults.Ok(LegalIdentityResponse.FromDomain(identity));
    }

    internal static async Task<IResult> Delete(
        int id,
        CustomersDbContext dbContext,
        ICustomerTimelineRecorder timelineRecorder,
        CancellationToken cancellationToken)
    {
        var customer = await dbContext.Customers.FirstOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (customer is null)
        {
            return TypedResults.NotFound();
        }

        if (customer.Identity is not null)
        {
            var before = CustomerSnapshot.From(customer);
            customer.Identity = null;
            customer.UpdatedAt = DateTimeOffset.UtcNow;
            timelineRecorder.RecordCustomerUpdated(customer, before);
            await dbContext.SaveChangesAsync(cancellationToken);
        }

        return TypedResults.NoContent();
    }

    private static bool TryParse(
        LegalIdentityRequest request,
        out LegalIdentity identity,
        out IReadOnlyDictionary<string, string[]> errors)
    {
        return LegalIdentity.TryCreate(
            request.Country,
            request.Type,
            request.Id,
            request.Name,
            request.Source,
            out identity,
            out errors);
    }
}