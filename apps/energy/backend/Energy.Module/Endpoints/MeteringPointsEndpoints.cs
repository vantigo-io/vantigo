using Vantigo.Contracts.AspNetCore.Authorization;
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
        group.MapGet("", GetMeteringPointsEndpoint.Handler).WithSummary("List metering points")
            .RequirePermission("energy:metering-points-view")
            .RequirePermission("energy:meters-view");
        group.MapPost("", CreateMeteringPointEndpoint.Handler).WithSummary("Create a metering point")
            .RequirePermission("energy:metering-points-manage")
            .RequirePermission("energy:metering-points-view")
            .RequirePermission("energy:meters-manage")
            .RequirePermission("energy:meters-view");
        group.MapGet("/{id:int}", GetMeteringPointEndpoint.Handler).WithName(GetMeteringPointRouteName).WithSummary("Get a metering point")
            .RequirePermission("energy:metering-points-view")
            .RequirePermission("energy:meters-view");
        group.MapPut("/{id:int}", UpdateMeteringPointEndpoint.Handler).WithSummary("Update a metering point")
            .RequirePermission("energy:metering-points-manage")
            .RequirePermission("energy:metering-points-view")
            .RequirePermission("energy:meters-view");
        group.MapGet("/{id:int}/meters", GetMetersEndpoint.Handler).WithSummary("List meter history")
            .RequirePermission("energy:meters-view");
        group.MapPost("/{id:int}/meters", ReplaceMeterEndpoint.Handler).WithSummary("Replace a meter")
            .RequirePermission("energy:meters-manage")
            .RequirePermission("energy:meters-view");
        group.MapGet("/{id:int}/consumption", GetConsumptionEndpoint.Handler).WithSummary("List current consumption intervals")
            .RequirePermission("energy:consumption-view");
        group.MapGet("/{id:int}/consumption/aggregate", GetConsumptionAggregateEndpoint.Handler).WithSummary("Aggregate consumption")
            .RequirePermission("energy:consumption-view");
        group.MapPost("/{id:int}/consumption", AddManualConsumptionEndpoint.Handler).WithSummary("Add a manual consumption interval")
            .RequirePermission("energy:consumption-manage")
            .RequirePermission("energy:consumption-view");
        group.MapGet("/{id:int}/supply-periods", GetSupplyPeriodsEndpoint.Handler).WithSummary("List supply periods")
            .RequirePermission("energy:supply-periods-view");
        group.MapPost("/{id:int}/supply-periods", CreateSupplyPeriodEndpoint.Handler).WithSummary("Create a supply period")
            .RequirePermission("energy:supply-periods-manage")
            .RequirePermission("energy:supply-periods-view");
        group.MapPost("/{id:int}/supply-periods/switch", SwitchSupplyPeriodEndpoint.Handler).WithSummary("Switch supply period customer")
            .RequirePermission("energy:supply-periods-manage")
            .RequirePermission("energy:supply-periods-view");
        group.MapPost("/{id:int}/supply-periods/{periodId:int}/end", EndSupplyPeriodEndpoint.Handler).WithSummary("End a supply period")
            .RequirePermission("energy:supply-periods-manage")
            .RequirePermission("energy:supply-periods-view");
        group.MapDelete("/{id:int}/supply-periods/{periodId:int}", CancelSupplyPeriodEndpoint.Handler).WithSummary("Cancel a supply period")
            .RequirePermission("energy:supply-periods-manage");
        return app;
    }
}