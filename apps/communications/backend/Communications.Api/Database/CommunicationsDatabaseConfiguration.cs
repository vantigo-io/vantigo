using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Communications.Api.Database.Accounts;
using Vantigo.Communications.Api.Database.Communications;

namespace Vantigo.Communications.Api.Database;

internal static class CommunicationsDatabaseConfiguration
{
    internal static IServiceCollection AddCommunicationsDatabases(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddSingleton<NpgsqlDataSource>(_ =>
        {
            var connectionString = configuration.GetConnectionString("Postgresql");
            if (string.IsNullOrWhiteSpace(connectionString)) throw new InvalidOperationException("ConnectionStrings:Postgresql is required.");
            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddDbContext<CommunicationsDbContext>((provider, options) => options.UseNpgsql(provider.GetRequiredService<NpgsqlDataSource>()));
        services.AddDbContext<AccountsDbContext>((provider, options) => options.UseNpgsql(
            provider.GetRequiredService<NpgsqlDataSource>(), npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "accounts")));
        return services;
    }

    internal static async Task MigrateCommunicationsDatabasesAsync(this WebApplication app)
        => await app.Services.MigrateCommunicationsDatabasesAsync();

    internal static async Task MigrateCommunicationsDatabasesAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        await scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>().Database.MigrateAsync();
        await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Database.MigrateAsync();
    }

    internal static async Task SeedConfiguredMailboxAsync(this WebApplication app)
        => await app.Services.SeedConfiguredMailboxAsync(app.Configuration);

    internal static async Task SeedConfiguredMailboxAsync(this IServiceProvider services, IConfiguration configuration)
    {
        var mailboxConfiguration = configuration.GetSection("Communications:BootstrapMailbox");
        if (!mailboxConfiguration.GetValue<bool>("Enabled")) return;
        var fromAddress = mailboxConfiguration["FromAddress"]?.Trim();
        if (string.IsNullOrWhiteSpace(fromAddress))
            throw new InvalidOperationException("Communications:BootstrapMailbox:FromAddress is required when mailbox bootstrap is enabled.");

        await using var scope = services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        if (await db.SharedMailboxes.AnyAsync()) return;
        db.SharedMailboxes.Add(new SharedMailbox
        {
            Id = Guid.NewGuid(),
            FromAddress = fromAddress,
            DisplayName = mailboxConfiguration["DisplayName"]?.Trim(),
            Provider = "smtp",
            IsDefault = true,
            CreatedAt = DateTimeOffset.UtcNow,
            IsActive = true,
        });
        await db.SaveChangesAsync();
    }
}