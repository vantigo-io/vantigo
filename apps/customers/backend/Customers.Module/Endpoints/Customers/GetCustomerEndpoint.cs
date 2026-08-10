using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Endpoints.Customers.Dtos;

namespace Vantigo.Customers.Endpoints.Customers;

/// <summary>
/// Retrieves a single customer by its id.
/// </summary>
internal static class GetCustomerEndpoint
{
    internal static async Task<Results<Ok<CustomerResponse>, NotFound>> Handler(
        int id,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var customer = await dbContext.Customers
            .AsNoTracking()
            .FirstOrDefaultAsync(c => c.Id == id, cancellationToken);

        if (customer is null)
        {
            return TypedResults.NotFound();
        }

        return TypedResults.Ok(CustomerResponse.FromDomain(customer));
    }
}