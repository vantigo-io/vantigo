using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Energy.Module.Tests.Integration;

[Collection(EnergyModuleCollection.Name)]
public sealed class EnergyEndpointsTests
{
    private readonly HttpClient _client;

    public EnergyEndpointsTests(EnergyApiFactory factory) => _client = factory.CreateAuthenticatedClient();

    [Fact]
    public async Task Metering_point_crud_round_trip()
    {
        var gsrn = Gsrn();
        var create = await _client.PostAsJsonAsync("/api/v1/energy/metering-points", NewMeteringPoint(gsrn));
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var point = await create.Content.ReadFromJsonAsync<MeteringPointResponse>();
        Assert.NotNull(point);
        var get = await _client.GetFromJsonAsync<MeteringPointResponse>($"/api/v1/energy/metering-points/{point.Id}");
        Assert.Equal(gsrn, get!.Gsrn);
        var update = await _client.PutAsJsonAsync($"/api/v1/energy/metering-points/{point.Id}", NewMeteringPoint(gsrn, "Updated meter"));
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        Assert.Equal("Updated meter", (await update.Content.ReadFromJsonAsync<MeteringPointResponse>())!.MeterNumber);
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

        var firstRows = await _client.GetFromJsonAsync<List<ConsumptionResponse>>("/api/v1/energy/customers/1001/consumption");
        var secondRows = await _client.GetFromJsonAsync<List<ConsumptionResponse>>("/api/v1/energy/customers/1002/consumption");
        Assert.Equal([1m], firstRows!.Select(row => row.QuantityKwh));
        Assert.Equal([2m], secondRows!.Select(row => row.QuantityKwh));
    }

    private async Task<MeteringPointResponse> CreatePointAsync()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/energy/metering-points", NewMeteringPoint(Gsrn()));
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return (await response.Content.ReadFromJsonAsync<MeteringPointResponse>())!;
    }

    private async Task AddConsumptionAsync(int pointId, DateTimeOffset start, DateTimeOffset end, decimal quantity) =>
        Assert.Equal(HttpStatusCode.OK, (await _client.PostAsJsonAsync($"/api/v1/energy/metering-points/{pointId}/consumption", new { start, end, quantityKwh = quantity })).StatusCode);

    private static object NewMeteringPoint(string gsrn, string meterNumber = "Test meter") => new
    {
        gsrn, meterNumber,
        address = new { streetAddress = "Testgata 1", postalCode = "0001", city = "Oslo", countryCode = "NO" },
        priceArea = "NO1", connectionStatus = "Connected",
    };

    private static string Gsrn() => $"7070575{Random.Shared.NextInt64(10000000000, 99999999999)}"[..18];
    private static DateTimeOffset UtcDate() => new(DateTime.UtcNow.Date, TimeSpan.Zero);

    private sealed record MeteringPointResponse(int Id, string Gsrn, string MeterNumber);
    private sealed record ConsumptionResponse(long Id, int MeteringPointId, DateTimeOffset Start, DateTimeOffset End, decimal QuantityKwh, string Quality, string Source, DateTimeOffset ReceivedAt);
    private sealed record SupplyPeriodResponse(int Id, int MeteringPointId, int CustomerId, DateTimeOffset Start, DateTimeOffset? End, string Status);
}
