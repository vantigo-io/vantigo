using Microsoft.AspNetCore.Builder;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Vantigo.Contracts.Authorization;
using Vantigo.Host;

namespace Vantigo.Host.Tests.Composition;

public sealed class HostCompositionTests
{
    public static IEnumerable<object[]> CommandModes() =>
        Enum.GetValues<VantigoCommand>()
            .Where(command => command is VantigoCommand.Api or VantigoCommand.Migrate or
                VantigoCommand.Seed or VantigoCommand.ResetCommunications)
            .Select(command => new object[] { command });

    [Theory]
    [MemberData(nameof(CommandModes))]
    public void Every_command_mode_builds_the_complete_service_graph(VantigoCommand command)
    {
        var builder = WebApplication.CreateBuilder(new WebApplicationOptions
        {
            EnvironmentName = Environments.Development,
            ApplicationName = typeof(global::Program).Assembly.GetName().Name,
        });
        builder.Configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["ConnectionStrings:vantigo"] = "Host=localhost;Port=5432;Database=vantigo;Username=vantigo;Password=vantigo",
            ["Modules:Customers:Enabled"] = "true",
            ["Modules:Communications:Enabled"] = "true",
            ["Modules:Products:Enabled"] = "true",
            ["Modules:Energy:Enabled"] = "true",
            ["Development:Seed:Enabled"] = "false",
        });
        builder.Host.UseDefaultServiceProvider((_, options) =>
        {
            options.ValidateOnBuild = true;
            options.ValidateScopes = true;
        });

        Program.RegisterHostServices(builder, command);

        using var app = builder.Build();
        Assert.NotNull(app.Services.GetRequiredService<IPermissionCatalog>());
    }

    [Fact]
    public async Task Database_commands_return_without_starting_the_api_or_hosted_workers()
    {
        var migrateCalled = false;
        var resetCalled = false;

        var result = await VantigoCommandDispatcher.ExecuteDatabaseCommandAsync(
            VantigoCommand.Migrate,
            () =>
            {
                migrateCalled = true;
                return Task.CompletedTask;
            },
            () =>
            {
                resetCalled = true;
                return Task.FromResult(true);
            });

        Assert.True(result);
        Assert.True(migrateCalled);
        Assert.False(resetCalled);
    }

    [Theory]
    [InlineData(VantigoCommand.Migrate)]
    [InlineData(VantigoCommand.Seed)]
    [InlineData(VantigoCommand.ResetCommunications)]
    public void Non_api_commands_return_before_the_api_pipeline(VantigoCommand command)
    {
        Assert.False(Program.IsApiCommand(command));
    }
}