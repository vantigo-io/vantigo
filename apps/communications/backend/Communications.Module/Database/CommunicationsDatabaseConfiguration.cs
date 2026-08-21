using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Communications.Authorization;
using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Endpoints;
using Vantigo.Communications.Services;
using Vantigo.Configuration;
using Vantigo.Contracts.Authorization;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Communications.Database;

public static class CommunicationsDatabaseConfiguration
{
    public static IServiceCollection AddCommunicationsModule(
        this IServiceCollection services,
        IConfiguration configuration)
    {
        services.AddVantigoTenancyEntityFramework();
        services.AddSingleton<IPermissionCatalogContributor, CommunicationsPermissionCatalogContributor>();
        services.TryAddSingleton<NpgsqlDataSource>(serviceProvider =>
        {
            var connectionStrings = serviceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
            var connectionString = connectionStrings.Resolve("communications");
            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddDbContext<CommunicationsDbContext>((provider, options) => options.UseNpgsql(
            provider.GetRequiredService<NpgsqlDataSource>(), npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "communications"))
            .UseTenancy(provider));
        services.AddCommunicationsModuleVersioning();
        services.AddHttpClient("mailgun", client => client.Timeout = TimeSpan.FromSeconds(10));
        services.AddOptions<MailgunInboundOptions>().BindConfiguration("Communications:Inbound");
        services.AddOptions<ClamAvOptions>().BindConfiguration("Communications:Scanner");
        services.AddOptions<CommunicationsAiOptions>().BindConfiguration("Communications:Ai");
        var aiSection = configuration.GetSection("Communications:Ai");
        // Registration is deliberately conditional: disabled or unconfigured AI must not
        // create a chat client, network client, or provider dependency in the host.
        if (aiSection?.GetValue<bool>("Enabled") == true &&
            string.Equals(aiSection["Provider"] ?? "openai", "openai", StringComparison.OrdinalIgnoreCase) &&
            !string.IsNullOrWhiteSpace(aiSection["ApiKey"]))
        {
            services.AddSingleton<Microsoft.Extensions.AI.IChatClient>(serviceProvider =>
            {
                var options = serviceProvider.GetRequiredService<IOptions<CommunicationsAiOptions>>().Value;
                return new OpenAI.OpenAIClient(options.ApiKey!).GetChatClient(options.Model).AsIChatClient();
            });
        }
        services.AddScoped<ICommunicationsAiService, CommunicationsAiService>();
        services.AddSingleton<MailboxCredentialProtector>();
        services.AddSingleton<IDnsResolver, SystemDnsResolver>();
        services.AddSingleton<ISmtpDestinationGuard, SmtpDestinationGuard>();
        services.AddSingleton<SmtpDeliveryProvider>();
        services.AddSingleton<MailgunDeliveryProvider>();
        services.AddSingleton<IEmailDeliveryProvider>(provider => provider.GetRequiredService<SmtpDeliveryProvider>());
        services.AddSingleton<IEmailDeliveryProvider>(provider => provider.GetRequiredService<MailgunDeliveryProvider>());
        services.AddScoped<IEmailSender, ProviderDispatchingEmailSender>();
        services.AddScoped<IOutboundChannelAdapter, EmailOutboundChannelAdapter>();
        services.AddScoped<IOutboundChannelAdapterRegistry, OutboundChannelAdapterRegistry>();
        services.AddScoped<IThreadResolver, EmailThreadResolver>();
        services.AddScoped<IConversationContactLinker, ConversationContactLinker>();
        services.AddScoped<OutboxJobProcessor>();
        services.AddScoped<RetentionCleanupService>();
        services.AddScoped<AttachmentCleanupService>();
        services.AddScoped<ICommunicationsObjectPurger, CommunicationsObjectPurger>();
        services.AddScoped<AttachmentScanProcessor>();
        services.AddScoped<InboundEmailJobProcessor>();
        services.AddHostedService<CommunicationsOutboxWorker>();
        services.AddHostedService<CommunicationsInboundWorker>();
        services.AddHostedService<CommunicationsRetentionWorker>();
        services.AddHostedService<CommunicationsAttachmentCleanupWorker>();
        var scannerSection = configuration.GetSection("Communications:Scanner");
        if (scannerSection?.GetValue<string>("Host") is { Length: > 0 })
            services.AddSingleton<IAttachmentScanner, ClamAvAttachmentScanner>();
        else
            services.AddSingleton<IAttachmentScanner, DisabledAttachmentScanner>();
        services.AddHostedService<CommunicationsAttachmentScannerWorker>();
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
        var tenant = await scope.ServiceProvider.GetRequiredService<ITenantDirectory>().GetDefaultTenantAsync();
        using var tenantScope = AmbientTenantContext.Enter(tenant);
        var options = scope.ServiceProvider.GetRequiredService<IOptions<CommunicationsOptions>>().Value;
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        if (options.BootstrapMailbox.Enabled)
        {
            var fromAddress = options.BootstrapMailbox.FromAddress?.Trim();
            if (string.IsNullOrWhiteSpace(fromAddress))
                throw new InvalidOperationException("Communications:BootstrapMailbox:FromAddress is required when mailbox bootstrap is enabled.");
            if (!await db.Channels.AnyAsync(cancellationToken))
            {
                db.Channels.Add(new Channel
                {
                    Id = Guid.NewGuid(),
                    Type = "email",
                    Address = fromAddress,
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