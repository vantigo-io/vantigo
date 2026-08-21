using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;

using Vantigo.Communications.Services;
using Vantigo.Host;

namespace Vantigo.Host.Tests.CommandLine;

public sealed class CommunicationsSchemaResetTests
{
    [Fact]
    public async Task ExecuteAsync_RejectsNonDevelopment()
    {
        var (services, resetter, migration) = CreateServices(communicationsEnabled: true);
        var error = new StringWriter();

        var result = await CommunicationsSchemaResetCommand.ExecuteAsync(
            services,
            new TestHostEnvironment("Production"),
            "true",
            error: error);

        Assert.False(result);
        Assert.Contains("only available in Development", error.ToString());
        Assert.Equal(0, resetter.Calls);
        Assert.Equal(0, migration.Calls);
    }

    [Fact]
    public async Task ExecuteAsync_RequiresExplicitConfirmation()
    {
        var (services, resetter, migration) = CreateServices(communicationsEnabled: true);
        var error = new StringWriter();

        var result = await CommunicationsSchemaResetCommand.ExecuteAsync(
            services,
            new TestHostEnvironment("Development"),
            confirmationValue: "false",
            error: error);

        Assert.False(result);
        Assert.Contains(CommunicationsSchemaResetCommand.ConfirmationEnvironmentVariable, error.ToString());
        Assert.Contains("No interactive confirmation", error.ToString());
        Assert.Equal(0, resetter.Calls);
        Assert.Equal(0, migration.Calls);
    }

    [Fact]
    public async Task ExecuteAsync_DropsOnlyCommunicationsSchemaAndMigrates()
    {
        var sqlExecutor = new RecordingSqlExecutor();
        var resetter = new CommunicationsSchemaResetter(sqlExecutor);
        var migration = new RecordingMigrationRunner();
        var purger = new RecordingObjectPurger();
        var serviceCollection = new ServiceCollection();
        serviceCollection.AddSingleton(new ModuleActivation(customers: true, communications: true, products: false, energy: false));
        serviceCollection.AddSingleton<ICommunicationsSchemaResetter>(resetter);
        serviceCollection.AddSingleton<ICommunicationsObjectPurger>(purger);
        serviceCollection.AddSingleton<ICommunicationsMigrationRunner>(migration);
        var services = serviceCollection.BuildServiceProvider();
        var output = new StringWriter();

        var result = await CommunicationsSchemaResetCommand.ExecuteAsync(
            services,
            new TestHostEnvironment("Development"),
            "true",
            output: output);

        Assert.True(result);
        Assert.Equal("DROP SCHEMA IF EXISTS \"communications\" CASCADE;", sqlExecutor.Sql);
        Assert.Equal(1, migration.Calls);
        Assert.Equal(1, purger.Calls);
        Assert.Contains("Communications data was destroyed", output.ToString());
    }

    [Fact]
    public async Task ExecuteAsync_resolves_scoped_purger_from_a_child_scope_with_validate_scopes()
    {
        var recorder = new ScopedPurgerRecorder();
        var serviceCollection = new ServiceCollection();
        serviceCollection.AddSingleton(new ModuleActivation(customers: true, communications: true, products: false, energy: false));
        serviceCollection.AddSingleton(recorder);
        serviceCollection.AddScoped<ICommunicationsObjectPurger, ScopedRecordingObjectPurger>();
        serviceCollection.AddSingleton<ICommunicationsSchemaResetter, RecordingResetter>();
        serviceCollection.AddSingleton<ICommunicationsMigrationRunner, RecordingMigrationRunner>();
        await using var services = serviceCollection.BuildServiceProvider(new ServiceProviderOptions { ValidateScopes = true, ValidateOnBuild = true });

        var result = await CommunicationsSchemaResetCommand.ExecuteAsync(
            services,
            new TestHostEnvironment("Development"),
            "true");

        Assert.True(result);
        Assert.Equal(1, recorder.Calls);
    }

    [Fact]
    public async Task ExecuteAsync_RejectsDisabledCommunicationsModule()
    {
        var services = CreateServices(communicationsEnabled: false).services;
        var error = new StringWriter();

        var result = await CommunicationsSchemaResetCommand.ExecuteAsync(
            services,
            new TestHostEnvironment("Development"),
            "true",
            error: error);

        Assert.False(result);
        Assert.Contains("Communications module is disabled", error.ToString());
    }

    [Fact]
    public async Task ExecuteAsync_Does_not_drop_schema_when_object_purge_fails()
    {
        var resetter = new RecordingResetter();
        var migration = new RecordingMigrationRunner();
        var services = new ServiceCollection();
        services.AddSingleton(new ModuleActivation(customers: true, communications: true, products: false, energy: false));
        services.AddSingleton<ICommunicationsSchemaResetter>(resetter);
        services.AddSingleton<ICommunicationsMigrationRunner>(migration);
        services.AddSingleton<ICommunicationsObjectPurger>(new FailingObjectPurger());

        await Assert.ThrowsAsync<InvalidOperationException>(() => CommunicationsSchemaResetCommand.ExecuteAsync(
            services.BuildServiceProvider(), new TestHostEnvironment("Development"), "true"));

        Assert.Equal(0, resetter.Calls);
        Assert.Equal(0, migration.Calls);
    }

    [Fact]
    public async Task ExecuteDatabaseCommandAsync_DoesNotResetForNormalMigrate()
    {
        var migrateCalls = 0;
        var resetCalls = 0;

        await VantigoCommandDispatcher.ExecuteDatabaseCommandAsync(
            VantigoCommand.Migrate,
            () =>
            {
                migrateCalls++;
                return Task.CompletedTask;
            },
            () =>
            {
                resetCalls++;
                return Task.FromResult(true);
            });

        Assert.Equal(1, migrateCalls);
        Assert.Equal(0, resetCalls);
    }

    private static (IServiceProvider services, RecordingResetter resetter, RecordingMigrationRunner migration) CreateServices(
        bool communicationsEnabled,
        RecordingResetter? resetter = null,
        RecordingMigrationRunner? migration = null)
    {
        resetter ??= new RecordingResetter();
        migration ??= new RecordingMigrationRunner();
        var serviceCollection = new ServiceCollection();
        serviceCollection.AddSingleton(new ModuleActivation(customers: true, communications: communicationsEnabled, products: false, energy: false));
        serviceCollection.AddSingleton<ICommunicationsSchemaResetter>(resetter);
        serviceCollection.AddSingleton<ICommunicationsMigrationRunner>(migration);
        serviceCollection.AddSingleton<ICommunicationsObjectPurger, RecordingObjectPurger>();
        return (serviceCollection.BuildServiceProvider(), resetter, migration);
    }

    private sealed class RecordingObjectPurger : ICommunicationsObjectPurger
    {
        public int Calls { get; private set; }
        public Task PurgeAsync(CancellationToken cancellationToken = default)
        {
            Calls++;
            return Task.CompletedTask;
        }
    }

    private sealed class FailingObjectPurger : ICommunicationsObjectPurger
    {
        public Task PurgeAsync(CancellationToken cancellationToken = default) =>
            Task.FromException(new InvalidOperationException("storage delete failed"));
    }

    private sealed class ScopedPurgerRecorder
    {
        public int Calls { get; set; }
    }

    private sealed class ScopedRecordingObjectPurger(ScopedPurgerRecorder recorder) : ICommunicationsObjectPurger
    {
        public Task PurgeAsync(CancellationToken cancellationToken = default)
        {
            recorder.Calls++;
            return Task.CompletedTask;
        }
    }

    private sealed class RecordingSqlExecutor : ICommunicationsSchemaResetSqlExecutor
    {
        public string? Sql { get; private set; }

        public Task ExecuteAsync(string sql, CancellationToken cancellationToken = default)
        {
            Sql = sql;
            return Task.CompletedTask;
        }
    }

    private sealed class RecordingResetter : ICommunicationsSchemaResetter
    {
        public int Calls { get; private set; }

        public Task DropSchemaAsync(CancellationToken cancellationToken = default)
        {
            Calls++;
            return Task.CompletedTask;
        }
    }

    private sealed class RecordingMigrationRunner : ICommunicationsMigrationRunner
    {
        public int Calls { get; private set; }

        public Task MigrateAsync(CancellationToken cancellationToken = default)
        {
            Calls++;
            return Task.CompletedTask;
        }
    }

    private sealed class TestHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;
        public string ApplicationName { get; set; } = nameof(CommunicationsSchemaResetTests);
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
    }
}