using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Customers.Api.Services;

internal static class ApplicationServiceCollectionExtensions
{
    internal static IServiceCollection AddApplicationEmail(
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

    internal static IServiceCollection AddCustomerTimeline(this IServiceCollection services)
    {
        services.AddScoped<ICustomerTimelineRecorder, CustomerTimelineRecorder>();
        return services;
    }
}