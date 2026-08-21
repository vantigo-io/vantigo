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

    public CommunicationsSmtpOptions Smtp { get; set; } = new();
}

/// <summary>
/// Controls which SMTP destinations a channel is allowed to connect to. A
/// delegated channel administrator only needs the module's
/// <c>ChannelsManage</c> permission to point outbound SMTP delivery at any
/// host, so by default private, link-local, loopback, and reserved address
/// ranges - including the cloud metadata address 169.254.169.254 - are
/// rejected, wherever the host resolves.
/// </summary>
public sealed class CommunicationsSmtpOptions
{
    /// <summary>
    /// Optional allowlist of approved SMTP hostnames. When non-empty, only
    /// hosts on this list may be used as an SMTP destination; a listed host is
    /// exempt from the private/reserved-range check below, since an installer
    /// who names it here has explicitly approved it (for example, an internal
    /// relay). Hosts are matched case-insensitively against the configured
    /// destination hostname, not the resolved address.
    /// </summary>
    public string[] AllowedHosts { get; set; } = [];

    /// <summary>
    /// Permits SMTP destinations that resolve to a private, link-local,
    /// loopback, or reserved address. Defaults to false; set true only for
    /// local development or a deployment where SMTP intentionally targets a
    /// host on a private network (for example, an internal relay reachable
    /// only from the deployment's own network).
    /// </summary>
    public bool AllowPrivateNetworks { get; set; }
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