using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Identity.Services;

public static class IdentityServiceCollectionExtensions
{
    public static IServiceCollection AddApplicationEmail(
        this IServiceCollection services,
        IConfiguration configuration)
    {
        services.Configure<EmailOptions>(configuration.GetSection("Email"));
        if (string.Equals(configuration["Email:Provider"], "Smtp", StringComparison.OrdinalIgnoreCase))
        {
            services.AddSingleton<IApplicationEmailSender, SmtpApplicationEmailSender>();
        }
        else
        {
            services.AddSingleton<IApplicationEmailSender, LoggingApplicationEmailSender>();
        }

        return services;
    }
}