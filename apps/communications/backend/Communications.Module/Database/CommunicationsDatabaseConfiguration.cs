using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Endpoints;
using Vantigo.Communications.Services;
using Vantigo.Configuration;

namespace Vantigo.Communications.Database;

public static class CommunicationsDatabaseConfiguration
{
    public static IServiceCollection AddCommunicationsModule(this IServiceCollection services)
    {
        services.TryAddSingleton<NpgsqlDataSource>(serviceProvider =>
        {
            var connectionStrings = serviceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
            var connectionString = connectionStrings.Resolve("communications");
            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddDbContext<CommunicationsDbContext>((provider, options) => options.UseNpgsql(
            provider.GetRequiredService<NpgsqlDataSource>(), npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "communications")));
        services.AddCommunicationsModuleVersioning();
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

    public static async Task SeedCommunicationsAsync(this IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
        var options = scope.ServiceProvider.GetRequiredService<IOptions<CommunicationsOptions>>().Value;
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        if (options.BootstrapMailbox.Enabled)
        {
            var fromAddress = options.BootstrapMailbox.FromAddress?.Trim();
            if (string.IsNullOrWhiteSpace(fromAddress))
                throw new InvalidOperationException("Communications:BootstrapMailbox:FromAddress is required when mailbox bootstrap is enabled.");
            if (!await db.SharedMailboxes.AnyAsync(cancellationToken))
            {
                db.SharedMailboxes.Add(new SharedMailbox
                {
                    Id = Guid.NewGuid(),
                    FromAddress = fromAddress,
                    DisplayName = options.BootstrapMailbox.DisplayName?.Trim(),
                    Provider = "smtp",
                    IsDefault = true,
                    CreatedAt = DateTimeOffset.UtcNow,
                    IsActive = true,
                });
                await db.SaveChangesAsync(cancellationToken);
            }
        }

        var seedOptions = scope.ServiceProvider.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value;
        if (seedOptions.Enabled)
            await DevelopmentSeed.DevelopmentDataSeeder.SeedDevelopmentDataAsync(services, cancellationToken);
    }
}