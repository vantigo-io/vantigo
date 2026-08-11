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
        group.MapPost("", CreateMeteringPointEndpoint.Handler).WithSummary("Create a metering point");
        group.MapGet("/{id:int}", GetMeteringPointEndpoint.Handler).WithName(GetMeteringPointRouteName).WithSummary("Get a metering point");
        group.MapPut("/{id:int}", UpdateMeteringPointEndpoint.Handler).WithSummary("Update a metering point");
        group.MapGet("/{id:int}/meters", GetMetersEndpoint.Handler).WithSummary("List meter history");
        group.MapPost("/{id:int}/meters", ReplaceMeterEndpoint.Handler).WithSummary("Replace a meter");
        group.MapGet("/{id:int}/consumption", GetConsumptionEndpoint.Handler).WithSummary("List current consumption intervals");
        group.MapGet("/{id:int}/consumption/aggregate", GetConsumptionAggregateEndpoint.Handler).WithSummary("Aggregate consumption");
        group.MapPost("/{id:int}/consumption", AddManualConsumptionEndpoint.Handler).WithSummary("Add a manual consumption interval");
        group.MapGet("/{id:int}/supply-periods", GetSupplyPeriodsEndpoint.Handler).WithSummary("List supply periods");
        group.MapPost("/{id:int}/supply-periods", CreateSupplyPeriodEndpoint.Handler).WithSummary("Create a supply period");
        group.MapPost("/{id:int}/supply-periods/switch", SwitchSupplyPeriodEndpoint.Handler).WithSummary("Switch supply period customer");
        group.MapPost("/{id:int}/supply-periods/{periodId:int}/end", EndSupplyPeriodEndpoint.Handler).WithSummary("End a supply period");
        group.MapDelete("/{id:int}/supply-periods/{periodId:int}", CancelSupplyPeriodEndpoint.Handler).WithSummary("Cancel a supply period");
        return app;
    }
}