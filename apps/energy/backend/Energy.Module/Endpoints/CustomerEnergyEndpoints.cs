using Vantigo.Energy.Endpoints.Customers;

namespace Vantigo.Energy.Endpoints;

internal static class CustomerEnergyEndpoints
{
    internal static IEndpointRouteBuilder MapCustomerEnergyEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/customers").WithTags("Customer energy");
        group.MapGet("/{customerId:int}/metering-points", GetCustomerMeteringPointsEndpoint.Handler).WithSummary("List a customer's metering points");
        group.MapGet("/{customerId:int}/consumption", GetCustomerConsumptionEndpoint.Handler).WithSummary("List a customer's consumption");
        return app;
    }
}