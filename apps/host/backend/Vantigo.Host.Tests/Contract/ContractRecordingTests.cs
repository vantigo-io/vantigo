using System.Net.Http.Json;
using System.Text.Json;

using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.TestHost;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Vantigo.Testing;

namespace Vantigo.Host.Tests.Contract;

[Collection(ContractRecordingCollection.Name)]
public sealed class ContractRecordingTests
{
    private static async Task<IHost> StartAsync()
    {
        var host = new HostBuilder().ConfigureWebHost(web => web
            .UseTestServer()
            .ConfigureServices(services => { services.AddRouting(); services.AddContractRecording(); })
            .Configure(app =>
            {
                app.UseRouting();
                app.UseEndpoints(endpoints =>
                {
                    endpoints.MapPost("/api/v1/echo", async (HttpRequest request) =>
                        Results.Json(new { received = await new StreamReader(request.Body).ReadToEndAsync() }, statusCode: 201));
                    endpoints.MapPost("/api/v1/large-echo", async (HttpRequest request) =>
                        Results.Json(new { received = await new StreamReader(request.Body).ReadToEndAsync() }, statusCode: 201));
                    endpoints.MapGet("/health/live", () => Results.Ok());
                });
            })).Build();
        await host.StartAsync();
        return host;
    }

    [Fact]
    public async Task Records_api_exchanges_as_json_lines_and_ignores_other_paths()
    {
        var dir = Directory.CreateTempSubdirectory("contract-recording-").FullName;
        var previous = Environment.GetEnvironmentVariable("VANTIGO_CONTRACT_RECORD");
        Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", dir);
        try
        {
            using var host = await StartAsync();
            var client = host.GetTestClient();
            var response = await client.PostAsJsonAsync("/api/v1/echo?x=1", new { name = "Acme" });
            Assert.Equal(201, (int)response.StatusCode);
            Assert.Contains("Acme", await response.Content.ReadAsStringAsync()); // the client still gets the body
            await client.GetAsync("/health/live");
            await host.StopAsync();

            var lines = Directory.GetFiles(dir, "*.jsonl").SelectMany(File.ReadAllLines).ToList();
            var line = Assert.Single(lines);
            using var json = JsonDocument.Parse(line);
            var root = json.RootElement;
            Assert.Equal("POST", root.GetProperty("method").GetString());
            Assert.Equal("/api/v1/echo", root.GetProperty("path").GetString());
            Assert.Equal("?x=1", root.GetProperty("query").GetString());
            Assert.Equal("{\"name\":\"Acme\"}", root.GetProperty("requestBody").GetString());
            Assert.Equal(201, root.GetProperty("status").GetInt32());
            Assert.StartsWith("application/json", root.GetProperty("responseContentType").GetString());
            Assert.Contains("Acme", root.GetProperty("responseBody").GetString());
        }
        finally
        {
            Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", previous);
            Directory.Delete(dir, recursive: true);
        }
    }

    [Fact]
    public async Task Calling_AddContractRecording_twice_still_records_each_exchange_once()
    {
        var dir = Directory.CreateTempSubdirectory("contract-recording-").FullName;
        var previous = Environment.GetEnvironmentVariable("VANTIGO_CONTRACT_RECORD");
        Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", dir);
        try
        {
            var host = new HostBuilder().ConfigureWebHost(web => web
                .UseTestServer()
                .ConfigureServices(services =>
                {
                    services.AddRouting();
                    // A factory wrapped with WithWebHostBuilder replays its parent's
                    // ConfigureWebHost (which already hooked AddContractRecording), so
                    // a second call here is a realistic double registration, not a
                    // contrived one.
                    services.AddContractRecording();
                    services.AddContractRecording();
                })
                .Configure(app =>
                {
                    app.UseRouting();
                    app.UseEndpoints(endpoints =>
                    {
                        endpoints.MapPost("/api/v1/echo", async (HttpRequest request) =>
                            Results.Json(new { received = await new StreamReader(request.Body).ReadToEndAsync() }, statusCode: 201));
                    });
                })).Build();
            using var _ = host;
            await host.StartAsync();
            var client = host.GetTestClient();
            var response = await client.PostAsJsonAsync("/api/v1/echo", new { name = "Acme" });
            Assert.Equal(201, (int)response.StatusCode);
            await host.StopAsync();

            var lines = Directory.GetFiles(dir, "*.jsonl").SelectMany(File.ReadAllLines).ToList();
            Assert.Single(lines);
        }
        finally
        {
            Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", previous);
            Directory.Delete(dir, recursive: true);
        }
    }

    [Fact]
    public async Task Does_nothing_without_the_environment_variable()
    {
        var previous = Environment.GetEnvironmentVariable("VANTIGO_CONTRACT_RECORD");
        Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", null);
        try
        {
            using var host = await StartAsync();
            var response = await host.GetTestClient().PostAsJsonAsync("/api/v1/echo", new { name = "Acme" });
            Assert.Equal(201, (int)response.StatusCode);
        }
        finally
        {
            Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", previous);
        }
    }

    [Fact]
    public async Task Bodies_larger_than_256KiB_are_recorded_as_null_but_the_endpoint_still_gets_the_full_body()
    {
        var dir = Directory.CreateTempSubdirectory("contract-recording-").FullName;
        var previous = Environment.GetEnvironmentVariable("VANTIGO_CONTRACT_RECORD");
        Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", dir);
        try
        {
            using var host = await StartAsync();
            var client = host.GetTestClient();
            // 256 KiB = 262144 bytes. A JSON string field padded past that guarantees
            // the serialized request body exceeds the cap.
            var largeValue = new string('a', 300_000);
            var requestJson = JsonSerializer.Serialize(new { name = largeValue });
            var response = await client.PostAsync(
                "/api/v1/large-echo",
                new StringContent(requestJson, System.Text.Encoding.UTF8, "application/json"));
            Assert.Equal(201, (int)response.StatusCode);
            var received = await response.Content.ReadFromJsonAsync<JsonElement>();
            // The endpoint still received the full body even though it was too large to record.
            Assert.Equal(requestJson, received.GetProperty("received").GetString());
            await host.StopAsync();

            var lines = Directory.GetFiles(dir, "*.jsonl").SelectMany(File.ReadAllLines).ToList();
            var line = Assert.Single(lines);
            using var json = JsonDocument.Parse(line);
            var root = json.RootElement;
            Assert.Equal(JsonValueKind.Null, root.GetProperty("requestBody").ValueKind);
        }
        finally
        {
            Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", previous);
            Directory.Delete(dir, recursive: true);
        }
    }
}

[CollectionDefinition(Name, DisableParallelization = true)]
public sealed class ContractRecordingCollection
{
    public const string Name = "ContractRecording";
}