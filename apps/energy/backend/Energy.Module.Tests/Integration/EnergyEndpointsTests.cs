using System.Net;
using System.Net.Http.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Domain.Consumption;
using Vantigo.Energy.Domain.SupplyPeriods;

namespace Vantigo.Energy.Module.Tests.Integration;

[Collection(EnergyModuleCollection.Name)]
public sealed class EnergyEndpointsTests
{
    private readonly HttpClient _client;
    private readonly EnergyApiFactory _factory;

    public EnergyEndpointsTests(EnergyApiFactory factory)
    {
        _factory = factory;
        _client = factory.CreateAuthenticatedClient();
    }

    [Fact]
    public async Task Metering_point_crud_round_trip()
    {
        var gsrn = Gsrn();
        var create = await _client.PostAsJsonAsync("/api/v1/energy/metering-points", NewMeteringPoint(gsrn));
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var point = await create.Content.ReadFromJsonAsync<MeteringPointResponse>();
        Assert.NotNull(point);
        var meters = await _client.GetFromJsonAsync<List<MeterResponse>>($"/api/v1/energy/metering-points/{point.Id}/meters");
        Assert.Single(meters!);
        Assert.Equal(point.MeterNumber, meters![0].MeterNumber);
        var get = await _client.GetFromJsonAsync<MeteringPointResponse>($"/api/v1/energy/metering-points/{point.Id}");
        Assert.Equal(gsrn, get!.Gsrn);
        var update = await _client.PutAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}", NewMeteringPoint(gsrn, "Updated meter"));
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        Assert.Equal("Test meter", (await update.Content.ReadFromJsonAsync<MeteringPointResponse>())!.MeterNumber);
    }

    [Fact]
    public async Task Meter_swap_closes_old_meter_and_keeps_ordered_history()
    {
        var point = await CreatePointAsync();
        var installedAt = UtcDate().AddDays(1);
        var swap = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/meters", new { meterNumber = "Replacement", installedAt });
        Assert.Equal(HttpStatusCode.Created, swap.StatusCode);
        var meters = await _client.GetFromJsonAsync<List<MeterResponse>>($"/api/v1/energy/metering-points/{point.Id}/meters");
        Assert.Equal(2, meters!.Count);
        Assert.True(meters[0].InstalledAt < meters[1].InstalledAt);
        Assert.Equal(installedAt, meters[0].RemovedAt);
        Assert.Null(meters[1].RemovedAt);
        var current = await _client.GetFromJsonAsync<MeteringPointResponse>($"/api/v1/energy/metering-points/{point.Id}");
        Assert.Equal("Replacement", current!.MeterNumber);
    }

    [Fact]
    public async Task Manual_consumption_supersedes_current_revision()
    {
        var point = await CreatePointAsync();
        var start = UtcDate().AddHours(1);
        var request = new { start, end = start.AddHours(1), quantityKwh = 2.5m };
        Assert.Equal(HttpStatusCode.OK, (await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/consumption", request)).StatusCode);
        var replacement = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/consumption", new { start, end = start.AddHours(1), quantityKwh = 3.5m });
        Assert.Equal(HttpStatusCode.OK, replacement.StatusCode);
        var rows = await _client.GetFromJsonAsync<List<ConsumptionResponse>>($"/api/v1/energy/metering-points/{point.Id}/consumption");
        Assert.Single(rows!);
        Assert.Equal(3.5m, rows![0].QuantityKwh);
    }

    [Fact]
    public async Task Supply_period_overlap_returns_conflict_and_end_allows_handover()
    {
        var point = await CreatePointAsync();
        var start = UtcDate().AddDays(-2);
        var first = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new { customerId = 1001, start });
        var firstBody = await first.Content.ReadAsStringAsync();
        Assert.True(first.StatusCode == HttpStatusCode.Created, firstBody);
        Assert.Equal(HttpStatusCode.Conflict, (await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new { customerId = 1002, start = start.AddDays(1) })).StatusCode);
        var period = await first.Content.ReadFromJsonAsync<SupplyPeriodResponse>();
        Assert.Equal(HttpStatusCode.OK, (await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods/{period!.Id}/end", new { end = start.AddDays(1) })).StatusCode);
        Assert.Equal(HttpStatusCode.Created, (await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new { customerId = 1002, start = start.AddDays(1) })).StatusCode);
    }

    [Fact]
    public async Task Supply_period_switch_ends_current_period_and_creates_contiguous_period()
    {
        var point = await CreatePointAsync();
        var start = Utc(2026, 4, 1);
        var switchAt = start.AddDays(3);
        var first = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new { customerId = 1001, start });
        var firstPeriod = await first.Content.ReadFromJsonAsync<SupplyPeriodResponse>();

        var response = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods/switch", new
        {
            customerId = 1002,
            switchAt,
        });

        var body = await response.Content.ReadAsStringAsync();
        Assert.True(response.StatusCode == HttpStatusCode.Created, body);
        var switched = await response.Content.ReadFromJsonAsync<SwitchSupplyPeriodResponse>();
        Assert.NotNull(switched);
        Assert.Equal(firstPeriod!.Id, switched!.EndedPeriod!.Id);
        Assert.Equal(SupplyPeriodStatus.Ended.ToString(), switched.EndedPeriod.Status);
        Assert.Equal(switchAt, switched.EndedPeriod.End);
        Assert.Equal(switchAt, switched.NewPeriod.Start);
        Assert.Null(switched.NewPeriod.End);
        Assert.Equal(1002, switched.NewPeriod.CustomerId);
    }

    [Fact]
    public async Task Supply_period_switch_without_active_period_behaves_as_move_in()
    {
        var point = await CreatePointAsync();

        var response = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods/switch", new
        {
            customerId = 1001,
            switchAt = Utc(2026, 5, 1),
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var switched = await response.Content.ReadFromJsonAsync<SwitchSupplyPeriodResponse>();
        Assert.NotNull(switched);
        Assert.Null(switched!.EndedPeriod);
        Assert.Equal(1001, switched.NewPeriod.CustomerId);
    }

    [Fact]
    public async Task Supply_period_switch_rejects_before_start_and_same_customer()
    {
        var point = await CreatePointAsync();
        var start = Utc(2026, 6, 1);
        var first = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new { customerId = 1001, start });
        Assert.Equal(HttpStatusCode.Created, first.StatusCode);

        var beforeStart = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods/switch", new
        {
            customerId = 1002,
            switchAt = start.AddMinutes(-1),
        });
        Assert.Equal(HttpStatusCode.BadRequest, beforeStart.StatusCode);

        var sameCustomer = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods/switch", new
        {
            customerId = 1001,
            switchAt = start.AddDays(1),
        });
        Assert.Equal(HttpStatusCode.BadRequest, sameCustomer.StatusCode);
    }

    [Fact]
    public async Task Supply_period_switch_rejects_overlap_with_historical_period()
    {
        var point = await CreatePointAsync();
        var start = Utc(2026, 7, 1);
        var first = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new { customerId = 1001, start });
        var period = await first.Content.ReadFromJsonAsync<SupplyPeriodResponse>();
        Assert.Equal(HttpStatusCode.OK, (await _client.PostAsJsonAsync(
            $"/api/v1/energy/metering-points/{point.Id}/supply-periods/{period!.Id}/end", new { end = start.AddDays(2) })).StatusCode);

        var response = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods/switch", new
        {
            customerId = 1002,
            switchAt = start.AddDays(1),
        });

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [Fact]
    public async Task Customer_consumption_after_switch_is_private_to_each_period()
    {
        var point = await CreatePointAsync();
        var switchAt = Utc(2026, 8, 1, 12);
        var first = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new
        {
            customerId = 1001,
            start = switchAt.AddHours(-2),
        });
        Assert.Equal(HttpStatusCode.Created, first.StatusCode);
        var switched = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods/switch", new
        {
            customerId = 1002,
            switchAt,
        });
        Assert.Equal(HttpStatusCode.Created, switched.StatusCode);

        await AddConsumptionAsync(point.Id, switchAt.AddHours(-2), switchAt.AddHours(-1), 1m);
        await AddConsumptionAsync(point.Id, switchAt.AddHours(-1), switchAt, 2m);
        await AddConsumptionAsync(point.Id, switchAt, switchAt.AddHours(1), 4m);
        await AddConsumptionAsync(point.Id, switchAt.AddHours(1), switchAt.AddHours(2), 8m);

        var oldCustomer = await _client.GetFromJsonAsync<List<ConsumptionResponse>>(
            $"/api/v1/energy/customers/1001/consumption?meteringPointId={point.Id}");
        var newCustomer = await _client.GetFromJsonAsync<List<ConsumptionResponse>>(
            $"/api/v1/energy/customers/1002/consumption?meteringPointId={point.Id}");
        Assert.Equal([1m, 2m], oldCustomer!.Select(interval => interval.QuantityKwh));
        Assert.Equal([4m, 8m], newCustomer!.Select(interval => interval.QuantityKwh));
    }

    [Fact]
    public async Task Customer_consumption_respects_supply_period_boundaries()
    {
        var point = await CreatePointAsync();
        var handover = UtcDate().AddDays(-1);
        var first = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new { customerId = 1001, start = handover.AddDays(-1) });
        var firstBody = await first.Content.ReadAsStringAsync();
        Assert.True(first.StatusCode == HttpStatusCode.Created, firstBody);
        var firstPeriod = await first.Content.ReadFromJsonAsync<SupplyPeriodResponse>();
        await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods/{firstPeriod!.Id}/end", new { end = handover });
        await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new { customerId = 1002, start = handover });
        await AddConsumptionAsync(point.Id, handover.AddHours(-2), handover.AddHours(-1), 1m);
        await AddConsumptionAsync(point.Id, handover.AddHours(1), handover.AddHours(2), 2m);
        await AddConsumptionAsync(point.Id, handover.AddHours(-1), handover.AddHours(1), 3m);

        var firstRows = await _client.GetFromJsonAsync<List<ConsumptionResponse>>($"/api/v1/energy/customers/1001/consumption?meteringPointId={point.Id}");
        var secondRows = await _client.GetFromJsonAsync<List<ConsumptionResponse>>($"/api/v1/energy/customers/1002/consumption?meteringPointId={point.Id}");
        Assert.Equal([1m], firstRows!.Select(row => row.QuantityKwh));
        Assert.Equal([2m], secondRows!.Select(row => row.QuantityKwh));
    }

    [Fact]
    public async Task Consumption_aggregate_uses_oslo_day_and_month_boundaries()
    {
        var point = await CreatePointAsync();
        await AddConsumptionAsync(point.Id, Utc(2026, 1, 5, 23, 30), Utc(2026, 1, 6, 0, 30), 1m);
        await AddConsumptionAsync(point.Id, Utc(2026, 1, 6, 23, 30), Utc(2026, 1, 7, 0, 30), 2m);
        await AddConsumptionAsync(point.Id, Utc(2026, 2, 1), Utc(2026, 2, 1, 1), 4m);

        var daily = await _client.GetFromJsonAsync<List<AggregateResponse>>(
            $"/api/v1/energy/metering-points/{point.Id}/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z&resolution=day");
        Assert.Equal(3, daily!.Count);
        Assert.Equal(1m, daily[0].QuantityKwh);
        Assert.Equal(new DateTimeOffset(2026, 1, 5, 23, 0, 0, TimeSpan.Zero), daily[0].BucketStart);
        Assert.Equal(2m, daily[1].QuantityKwh);

        var monthly = await _client.GetFromJsonAsync<List<AggregateResponse>>(
            $"/api/v1/energy/metering-points/{point.Id}/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z&resolution=month");
        Assert.Equal(2, monthly!.Count);
        Assert.Equal([3m, 4m], monthly.Select(item => item.QuantityKwh));
    }

    [Fact]
    public async Task Consumption_aggregate_marks_estimated_and_handles_oslo_dst_day()
    {
        var point = await CreatePointAsync();
        for (var hour = 0; hour < 23; hour++)
            await AddElhubConsumptionAsync(point.Id, Utc(2026, 3, 28, 23).AddHours(hour), Utc(2026, 3, 28, 23).AddHours(hour + 1), 1m);

        var rows = await _client.GetFromJsonAsync<List<AggregateResponse>>(
            $"/api/v1/energy/metering-points/{point.Id}/consumption/aggregate?from=2026-03-28T23:00:00Z&to=2026-03-29T22:00:00Z&resolution=day");
        var row = Assert.Single(rows!);
        Assert.Equal(23m, row.QuantityKwh);
        Assert.Equal(23, row.IntervalCount);
        Assert.True(row.HasEstimated);
        Assert.Equal(new DateTimeOffset(2026, 3, 28, 23, 0, 0, TimeSpan.Zero), row.BucketStart);
        Assert.Equal(new DateTimeOffset(2026, 3, 29, 22, 0, 0, TimeSpan.Zero), row.BucketEnd);
    }

    [Fact]
    public async Task Customer_consumption_aggregate_excludes_intervals_outside_supply_period()
    {
        var point = await CreatePointAsync();
        var start = Utc(2026, 1, 1);
        var end = Utc(2026, 1, 3);
        var period = await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods", new
        {
            customerId = 1001,
            start,
        });
        var periodResponse = await period.Content.ReadFromJsonAsync<SupplyPeriodResponse>();
        await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}/supply-periods/{periodResponse!.Id}/end", new { end });
        await AddConsumptionAsync(point.Id, start.AddHours(-1), start, 1m);
        await AddConsumptionAsync(point.Id, start.AddHours(1), start.AddHours(2), 2m);
        await AddConsumptionAsync(point.Id, end, end.AddHours(1), 4m);

        var rows = await _client.GetFromJsonAsync<List<CustomerAggregateResponse>>(
            $"/api/v1/energy/customers/1001/consumption/aggregate?meteringPointId={point.Id}&from=2026-01-01T00:00:00Z&to=2026-01-04T00:00:00Z&resolution=day");
        var row = Assert.Single(rows!);
        Assert.Equal(point.Id, row.MeteringPointId);
        Assert.Equal(2m, row.QuantityKwh);
    }

    [Fact]
    public async Task Consumption_aggregate_rejects_invalid_resolution()
    {
        var point = await CreatePointAsync();
        var response = await _client.GetAsync(
            $"/api/v1/energy/metering-points/{point.Id}/consumption/aggregate?from=2026-01-01&to=2026-01-02&resolution=week");
        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    private async Task<MeteringPointResponse> CreatePointAsync()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/energy/metering-points", NewMeteringPoint(Gsrn()));
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return (await response.Content.ReadFromJsonAsync<MeteringPointResponse>())!;
    }

    private async Task AddConsumptionAsync(int pointId, DateTimeOffset start, DateTimeOffset end, decimal quantity) =>
        Assert.Equal(HttpStatusCode.OK, (await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{pointId}/consumption", new { start, end, quantityKwh = quantity })).StatusCode);

    private async Task AddElhubConsumptionAsync(int pointId, DateTimeOffset start, DateTimeOffset end, decimal quantity)
    {
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<EnergyDbContext>();
        db.ConsumptionIntervals.Add(new ConsumptionInterval
        {
            MeteringPointId = pointId,
            Start = start,
            End = end,
            QuantityKwh = quantity,
            Quality = ConsumptionQuality.Estimated,
            Source = ConsumptionSource.Elhub,
            ReceivedAt = DateTimeOffset.UtcNow,
        });
        await db.SaveChangesAsync();
    }

    private static object NewMeteringPoint(string gsrn, string meterNumber = "Test meter") => new
    {
        gsrn, meterNumber,
        address = new { streetAddress = "Testgata 1", postalCode = "0001", city = "Oslo", countryCode = "NO" },
        priceArea = "NO1", connectionStatus = "Connected",
    };

    private static string Gsrn() => $"7070575{Random.Shared.NextInt64(10000000000, 99999999999)}"[..18];
    private static DateTimeOffset UtcDate() => new(DateTime.UtcNow.Date, TimeSpan.Zero);
    private static DateTimeOffset Utc(int year, int month, int day, int hour = 0, int minute = 0) =>
        new(year, month, day, hour, minute, 0, TimeSpan.Zero);

    private sealed record MeteringPointResponse(int Id, string Gsrn, string MeterNumber);
    private sealed record MeterResponse(int Id, int MeteringPointId, string MeterNumber, DateTimeOffset InstalledAt, DateTimeOffset? RemovedAt);
    private sealed record ConsumptionResponse(long Id, int MeteringPointId, DateTimeOffset Start, DateTimeOffset End, decimal QuantityKwh, string Quality, string Source, DateTimeOffset ReceivedAt);
    private sealed record SupplyPeriodResponse(int Id, int MeteringPointId, int CustomerId, DateTimeOffset Start, DateTimeOffset? End, string Status);
    private sealed record SwitchSupplyPeriodResponse(SupplyPeriodResponse? EndedPeriod, SupplyPeriodResponse NewPeriod);
    private sealed record AggregateResponse(DateTimeOffset BucketStart, DateTimeOffset BucketEnd, decimal QuantityKwh, long IntervalCount, bool HasEstimated);
    private sealed record CustomerAggregateResponse(int MeteringPointId, DateTimeOffset BucketStart, DateTimeOffset BucketEnd, decimal QuantityKwh, long IntervalCount, bool HasEstimated);
}
