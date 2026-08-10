using Vantigo.Energy.Endpoints.Consumption;
using Vantigo.Energy.Endpoints.MeteringPoints;
using Vantigo.Energy.Endpoints.SupplyPeriods;

namespace Vantigo.Energy.Endpoints;

internal static class MeteringPointsEndpoints
{
    internal const string GetMeteringPointRouteName = "GetEnergyMeteringPoint";

    internal static IEndpointRouteBuilder MapMeteringPointEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/metering-points").WithTags("Energy metering points");
        group.MapGet("", GetMeteringPointsEndpoint.Handler).WithSummary("List metering points");
        group.MapPost("", CreateMeteringPointEndpoint.Handler).RequireAntiforgery().WithSummary("Create a metering point");
        group.MapGet("/{id:int}", GetMeteringPointEndpoint.Handler).WithName(GetMeteringPointRouteName).WithSummary("Get a metering point");
        group.MapPut("/{id:int}", UpdateMeteringPointEndpoint.Handler).RequireAntiforgery().WithSummary("Update a metering point");
        group.MapGet("/{id:int}/consumption", GetConsumptionEndpoint.Handler).WithSummary("List current consumption intervals");
        group.MapPost("/{id:int}/consumption", AddManualConsumptionEndpoint.Handler).RequireAntiforgery().WithSummary("Add a manual consumption interval");
        group.MapGet("/{id:int}/supply-periods", GetSupplyPeriodsEndpoint.Handler).WithSummary("List supply periods");
        group.MapPost("/{id:int}/supply-periods", CreateSupplyPeriodEndpoint.Handler).RequireAntiforgery().WithSummary("Create a supply period");
        group.MapPost("/{id:int}/supply-periods/{periodId:int}/end", EndSupplyPeriodEndpoint.Handler).RequireAntiforgery().WithSummary("End a supply period");
        group.MapDelete("/{id:int}/supply-periods/{periodId:int}", CancelSupplyPeriodEndpoint.Handler).RequireAntiforgery().WithSummary("Cancel a supply period");
        return app;
    }
}