using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Communications.Database;
using Vantigo.Communications.Services;
using Vantigo.Configuration;

namespace Vantigo.Host;

public interface ICommunicationsSchemaResetSqlExecutor
{
    Task ExecuteAsync(string sql, CancellationToken cancellationToken = default);
}

public sealed class NpgsqlCommunicationsSchemaResetSqlExecutor(NpgsqlDataSource dataSource)
    : ICommunicationsSchemaResetSqlExecutor
{
    public async Task ExecuteAsync(string sql, CancellationToken cancellationToken = default)
    {
        await using var connection = await dataSource.OpenConnectionAsync(cancellationToken);
        await using var command = connection.CreateCommand();
        command.CommandText = sql;
        await command.ExecuteNonQueryAsync(cancellationToken);
    }
}

public interface ICommunicationsSchemaResetter
{
    Task DropSchemaAsync(CancellationToken cancellationToken = default);
}

public sealed class CommunicationsSchemaResetter(ICommunicationsSchemaResetSqlExecutor sqlExecutor)
    : ICommunicationsSchemaResetter
{
    public const string DropSchemaSql = "DROP SCHEMA IF EXISTS \"communications\" CASCADE;";

    public Task DropSchemaAsync(CancellationToken cancellationToken = default) =>
        sqlExecutor.ExecuteAsync(DropSchemaSql, cancellationToken);
}

public interface ICommunicationsMigrationRunner
{
    Task MigrateAsync(CancellationToken cancellationToken = default);
}

public sealed class CommunicationsMigrationRunner(IServiceProvider services) : ICommunicationsMigrationRunner
{
    public async Task MigrateAsync(CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        await services.MigrateCommunicationsAsync();
    }
}

public static class CommunicationsSchemaResetCommand
{
    public const string CommandName = "reset-communications";
    public const string ConfirmationEnvironmentVariable = "VANTIGO_CONFIRM_RESET_COMMUNICATIONS";

    public static async Task<bool> ExecuteAsync(
        IServiceProvider services,
        IHostEnvironment environment,
        string? confirmationValue = null,
        TextWriter? output = null,
        TextWriter? error = null,
        CancellationToken cancellationToken = default)
    {
        output ??= Console.Out;
        error ??= Console.Error;

        if (!environment.IsDevelopment())
        {
            await error.WriteLineAsync("The reset-communications command is only available in Development.");
            return false;
        }

        var modules = services.GetRequiredService<IOptions<ModuleHostingOptions>>().Value;
        if (!modules.Communications.Enabled)
        {
            await error.WriteLineAsync("The reset-communications command cannot run because the Communications module is disabled.");
            return false;
        }

        confirmationValue ??= Environment.GetEnvironmentVariable(ConfirmationEnvironmentVariable);
        if (!string.Equals(confirmationValue, "true", StringComparison.OrdinalIgnoreCase))
        {
            await error.WriteLineAsync(
                $"The reset-communications command requires {ConfirmationEnvironmentVariable}=true. " +
                "No interactive confirmation is supported.");
            return false;
        }

        // Purge through the typed Communications store before dropping metadata.
        // Any storage failure propagates and leaves the schema available for retry.
        // ICommunicationsObjectPurger owns a Communications DbContext and is scoped.
        // Never resolve it from the root provider (ValidateScopes must remain useful).
        await using (var scope = services.CreateAsyncScope())
            await scope.ServiceProvider.GetRequiredService<ICommunicationsObjectPurger>().PurgeAsync(cancellationToken);
        await services.GetRequiredService<ICommunicationsSchemaResetter>().DropSchemaAsync(cancellationToken);
        await output.WriteLineAsync(
            "WARNING: Communications data was destroyed. PostgreSQL schema \"communications\" was dropped with CASCADE; " +
            "normal Communications migrations are now being applied.");
        await services.GetRequiredService<ICommunicationsMigrationRunner>().MigrateAsync(cancellationToken);
        await output.WriteLineAsync("Communications schema reset completed successfully.");
        return true;
    }
}