using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

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
}