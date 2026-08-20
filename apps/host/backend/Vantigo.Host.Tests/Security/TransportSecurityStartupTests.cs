using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.HostFiltering;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Host;

namespace Vantigo.Host.Tests.Security;

/// <summary>
/// Boots the real host service graph outside Development and runs the startup
/// option validators, so the fail-closed transport rules are exercised through
/// the composition the application actually uses rather than a hand-built one.
/// </summary>
public sealed class TransportSecurityStartupTests
{
    private const string SecureConnectionString =
        "Host=db;Port=5432;Database=vantigo;Username=vantigo;Password=vantigo;SSL Mode=VerifyFull";

    [Fact]
    public void AProductionConfigurationThatMeetsTheTransportRules_Validates() =>
        Validate(Settings());

    [Fact]
    public void AnHttpPublicOrigin_FailsStartup()
    {
        Dictionary<string, string?> settings = Settings();
        settings["App:PublicOrigin"] = "http://vantigo.example.com";

        Exception exception = Assert.ThrowsAny<Exception>(() => Validate(settings));

        Assert.Contains("App:PublicOrigin", exception.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public void APostgresConnectionWithoutCertificateVerifiedTls_FailsStartup()
    {
        Dictionary<string, string?> settings = Settings();
        settings["ConnectionStrings:vantigo"] =
            "Host=db;Port=5432;Database=vantigo;Username=vantigo;Password=vantigo;SSL Mode=Disable";

        Exception exception = Assert.ThrowsAny<Exception>(() => Validate(settings));

        Assert.Contains("ConnectionStrings:vantigo", exception.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public void PlaintextSmtp_FailsStartup()
    {
        Dictionary<string, string?> settings = Settings();
        settings["Email:Smtp:EnableSsl"] = "false";
        settings["Email:Smtp:Port"] = "25";

        Exception exception = Assert.ThrowsAny<Exception>(() => Validate(settings));

        Assert.Contains("Email:Smtp must negotiate TLS", exception.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public void TheEscapeHatch_PermitsAnHttpOriginAndAPlaintextDatabaseConnection()
    {
        Dictionary<string, string?> settings = Settings();
        settings["App:PublicOrigin"] = "http://localhost:8080";
        settings["ConnectionStrings:vantigo"] =
            "Host=postgres;Database=vantigo;Username=vantigo;Password=vantigo";
        settings["Security:AllowInsecureTransport"] = "true";

        Validate(settings);
    }

    [Fact]
    public void HostFiltering_AcceptsOnlyThePublicOriginAndLoopback()
    {
        using WebApplication app = Build(Settings());

        HostFilteringOptions options = app.Services.GetRequiredService<IOptions<HostFilteringOptions>>().Value;

        Assert.Equal(["vantigo.example.com", "localhost", "127.0.0.1", "[::1]"], options.AllowedHosts);
    }

    [Fact]
    public void HostFiltering_LeavesAnExplicitAllowlistAlone()
    {
        Dictionary<string, string?> settings = Settings();
        settings["AllowedHosts"] = "vantigo.example.com;ops.example.com";

        using WebApplication app = Build(settings);

        HostFilteringOptions options = app.Services.GetRequiredService<IOptions<HostFilteringOptions>>().Value;

        Assert.Equal(["vantigo.example.com", "ops.example.com"], options.AllowedHosts);
    }

    /// <summary>
    /// A production configuration that satisfies every other startup validator, so
    /// each test can perturb exactly the one rule it is about. Every fail-closed
    /// validator the host gains needs a line here; when a test in this file starts
    /// failing with an unrelated "refuses to start outside Development" message,
    /// that is a new validator asking to be opted into deliberately, not a reason
    /// to weaken it.
    /// </summary>
    private static Dictionary<string, string?> Settings() => new()
    {
        ["ConnectionStrings:vantigo"] = SecureConnectionString,
        ["App:PublicOrigin"] = "https://vantigo.example.com",
        ["Authentication:Bootstrap:Secret"] = "transport-security-startup-test-secret",
        ["Email:Provider"] = "Smtp",
        ["Email:Smtp:Host"] = "smtp.example.com",
        // There is no Key Vault to wrap the Data Protection key ring with in this
        // test host, and wrapping is not what these tests are about.
        ["DataProtection:AllowUnwrappedKeys"] = "true",
        ["Modules:Customers:Enabled"] = "true",
        ["Modules:Communications:Enabled"] = "false",
        ["Modules:Products:Enabled"] = "false",
        ["Modules:Energy:Enabled"] = "false",
        ["Development:Seed:Enabled"] = "false",
    };

    private static WebApplication Build(Dictionary<string, string?> settings)
    {
        WebApplicationBuilder builder = WebApplication.CreateBuilder(new WebApplicationOptions
        {
            EnvironmentName = Environments.Production,
            ApplicationName = typeof(global::Program).Assembly.GetName().Name,
        });
        builder.Configuration.AddInMemoryCollection(settings);
        Program.RegisterHostServices(builder, VantigoCommand.Api);
        return builder.Build();
    }

    private static void Validate(Dictionary<string, string?> settings)
    {
        using WebApplication app = Build(settings);
        app.Services.GetRequiredService<IStartupValidator>().Validate();
    }
}