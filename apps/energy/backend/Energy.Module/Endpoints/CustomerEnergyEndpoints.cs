using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Energy.Endpoints.Customers;

namespace Vantigo.Energy.Endpoints;

internal static class CustomerEnergyEndpoints
{
    internal static IEndpointRouteBuilder MapCustomerEnergyEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/customers").WithTags("Customer energy");
        group.MapGet("/{customerId:int}/metering-points", GetCustomerMeteringPointsEndpoint.Handler).WithSummary("List a customer's metering points")
            .RequirePermission("energy:metering-points-view")
            .RequirePermission("energy:meters-view")
            .RequirePermission("energy:supply-periods-view");
        group.MapGet("/{customerId:int}/consumption", GetCustomerConsumptionEndpoint.Handler).WithSummary("List a customer's consumption")
            .RequirePermission("energy:consumption-view");
        group.MapGet("/{customerId:int}/consumption/aggregate", GetCustomerConsumptionAggregateEndpoint.Handler).WithSummary("Aggregate a customer's consumption")
            .RequirePermission("energy:consumption-view");
        return app;
    }
}