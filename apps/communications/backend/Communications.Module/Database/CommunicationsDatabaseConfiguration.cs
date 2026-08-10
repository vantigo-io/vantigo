using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection.Extensions;

using Npgsql;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Endpoints;
using Vantigo.Communications.Services;

namespace Vantigo.Communications.Database;

public static class CommunicationsDatabaseConfiguration
{
    public static IServiceCollection AddCommunicationsModule(this IServiceCollection services, IConfiguration configuration)
    {
        services.TryAddSingleton<NpgsqlDataSource>(_ =>
        {
            var connectionString = configuration.GetConnectionString("vantigo") ??
                configuration.GetConnectionString("communications") ??
                configuration.GetConnectionString("Postgresql");
            if (string.IsNullOrWhiteSpace(connectionString)) throw new InvalidOperationException("ConnectionStrings:vantigo is required.");
            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddDbContext<CommunicationsDbContext>((provider, options) => options.UseNpgsql(
            provider.GetRequiredService<NpgsqlDataSource>(), npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "communications")));
        services.AddCommunicationsModuleVersioning();
        services.AddDataProtection();
        services.AddHttpClient("mailgun", client => client.Timeout = TimeSpan.FromSeconds(10));
        services.AddSingleton<MailboxCredentialProtector>();
        services.AddSingleton<SmtpDeliveryProvider>();
        services.AddSingleton<MailgunDeliveryProvider>();
        services.AddSingleton<IEmailDeliveryProvider>(provider => provider.GetRequiredService<SmtpDeliveryProvider>());
        services.AddSingleton<IEmailDeliveryProvider>(provider => provider.GetRequiredService<MailgunDeliveryProvider>());
        services.AddScoped<IEmailSender, ProviderDispatchingEmailSender>();
        services.AddScoped<OutboxJobProcessor>();
        services.AddScoped<RetentionCleanupService>();
        services.AddHostedService<CommunicationsOutboxWorker>();
        services.AddHostedService<CommunicationsRetentionWorker>();
        return services;
    }

    public static async Task MigrateCommunicationsAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        await scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>().Database.MigrateAsync();
    }

    public static async Task SeedCommunicationsAsync(this IServiceProvider services, IConfiguration configuration, CancellationToken cancellationToken = default)
    {
        var mailboxConfiguration = configuration.GetSection("Communications:BootstrapMailbox");
        await using var scope = services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        if (mailboxConfiguration.GetValue<bool>("Enabled"))
        {
            var fromAddress = mailboxConfiguration["FromAddress"]?.Trim();
            if (string.IsNullOrWhiteSpace(fromAddress))
                throw new InvalidOperationException("Communications:BootstrapMailbox:FromAddress is required when mailbox bootstrap is enabled.");
            if (!await db.SharedMailboxes.AnyAsync(cancellationToken))
            {
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
                await db.SaveChangesAsync(cancellationToken);
            }
        }
        if (configuration.GetValue("Development:Seed:Enabled", true))
            await DevelopmentSeed.DevelopmentDataSeeder.SeedDevelopmentDataAsync(services, configuration, cancellationToken);
    }
}