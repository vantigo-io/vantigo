using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Communications module options.
/// </summary>
public sealed class CommunicationsOptions
{
    public OutboxOptions Outbox { get; set; } = new();

    public CommunicationsRetentionOptions Retention { get; set; } = new();

    public CommunicationsBootstrapMailboxOptions BootstrapMailbox { get; set; } = new();
}

public sealed class OutboxOptions
{
    public int LeaseSeconds { get; set; } = 60;
    public int ClaimAttempts { get; set; } = 10;
    public int MaxAttempts { get; set; } = 8;
    public int PollSeconds { get; set; } = 5;
}

public sealed class CommunicationsRetentionOptions
{
    public int Days { get; set; } = 365;
    public int BatchSize { get; set; } = 100;
    public int PollMinutes { get; set; } = 60;
}

public sealed class CommunicationsBootstrapMailboxOptions
{
    public bool Enabled { get; set; }
    public string? FromAddress { get; set; }
    public string? DisplayName { get; set; }
}

public static class CommunicationsConfigurationExtensions
{
    public static IServiceCollection AddCommunicationsOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<CommunicationsOptions>(configuration.GetSection("Communications"));
        services.Configure<OutboxOptions>(configuration.GetSection("Outbox"));
        return services;
    }
}