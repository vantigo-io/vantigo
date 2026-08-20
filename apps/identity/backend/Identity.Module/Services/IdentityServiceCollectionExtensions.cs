using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Identity.Services;

public static class IdentityServiceCollectionExtensions
{
    public static IServiceCollection AddApplicationEmail(this IServiceCollection services)
    {
        services.AddSingleton<IApplicationEmailSender>(serviceProvider =>
        {
            var options = serviceProvider.GetRequiredService<IOptions<EmailOptions>>().Value;
            return string.Equals(options.Provider, "Smtp", StringComparison.OrdinalIgnoreCase)
                ? new SmtpApplicationEmailSender(
                    serviceProvider.GetRequiredService<IOptions<EmailOptions>>(),
                    serviceProvider.GetRequiredService<ILogger<SmtpApplicationEmailSender>>())
                : new LoggingApplicationEmailSender(
                    serviceProvider.GetRequiredService<ILogger<LoggingApplicationEmailSender>>());
        });

        return services;
    }

    /// <summary>Registers identity's tenant control-plane services.</summary>
    public static IServiceCollection AddVantigoIdentityTenancy(this IServiceCollection services)
    {
        services.AddScoped<TenantDirectory>();
        services.AddScoped<ITenantDirectory>(provider => provider.GetRequiredService<TenantDirectory>());
        services.AddScoped<TenantMembershipService>();
        services.AddScoped<TenantBootstrapper>();
        services.AddScoped<SystemAdminBootstrapper>();
        return services;
    }
}