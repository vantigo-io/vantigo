using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration.Tests;

public sealed class PostgresTransportSecurityTests
{
    [Theory]
    [InlineData("Host=db;Database=vantigo;SSL Mode=VerifyFull")]
    [InlineData("Host=db;Database=vantigo;sslmode=verifyfull")]
    [InlineData("Host=db;Database=vantigo;SslMode=VerifyCA")]
    [InlineData("Host=db;Database=vantigo;SSL Mode=VerifyCA;Root Certificate=/etc/ssl/ca.pem")]
    public void CertificateVerifiedModes_AreAccepted(string connectionString) =>
        Assert.Null(TransportSecurityOptions.DescribeInsecurePostgresTransport(connectionString, "ConnectionStrings:vantigo"));

    [Theory]
    [InlineData("Host=db;Database=vantigo")] // Npgsql defaults to SSL Mode=Prefer
    [InlineData("Host=db;Database=vantigo;SSL Mode=Disable")]
    [InlineData("Host=db;Database=vantigo;SSL Mode=Prefer")]
    [InlineData("Host=db;Database=vantigo;SSL Mode=Allow")]
    [InlineData("Host=db;Database=vantigo;SSL Mode=Require")]
    public void ModesThatDoNotVerifyTheCertificate_AreRejected(string connectionString)
    {
        string? failure = TransportSecurityOptions.DescribeInsecurePostgresTransport(
            connectionString, "ConnectionStrings:vantigo");

        Assert.NotNull(failure);
        Assert.Contains("ConnectionStrings:vantigo", failure, StringComparison.Ordinal);
        Assert.Contains("Security:AllowInsecureTransport", failure, StringComparison.Ordinal);
    }

    [Fact]
    public void TrustServerCertificate_DefeatsVerificationAndIsRejected()
    {
        string? failure = TransportSecurityOptions.DescribeInsecurePostgresTransport(
            "Host=db;Database=vantigo;SSL Mode=VerifyFull;Trust Server Certificate=true",
            "ConnectionStrings:vantigo");

        Assert.NotNull(failure);
        Assert.Contains("Trust Server Certificate", failure, StringComparison.Ordinal);
    }

    [Fact]
    public void QuotedPasswordContainingASeparator_DoesNotConfuseTheParser() =>
        Assert.Null(TransportSecurityOptions.DescribeInsecurePostgresTransport(
            "Host=db;Database=vantigo;Password='pa;ss=word';SSL Mode=VerifyFull",
            "ConnectionStrings:vantigo"));

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void AnEmptyConnectionString_IsNotThisValidatorsProblem(string? connectionString) =>
        Assert.Null(TransportSecurityOptions.DescribeInsecurePostgresTransport(connectionString, "ConnectionStrings:vantigo"));
}

public sealed class ConnectionStringsTransportValidationTests
{
    [Fact]
    public void Production_WithoutCertificateVerifiedTls_FailsStartupValidation()
    {
        ServiceProvider provider = Build(
            Environments.Production,
            new Dictionary<string, string?> { ["ConnectionStrings:vantigo"] = "Host=db;Database=vantigo;SSL Mode=Disable" });

        OptionsValidationException exception = Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value);

        Assert.Contains("ConnectionStrings:vantigo", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void Production_WithTheEscapeHatch_Starts()
    {
        ServiceProvider provider = Build(
            Environments.Production,
            new Dictionary<string, string?>
            {
                ["ConnectionStrings:vantigo"] = "Host=db;Database=vantigo;SSL Mode=Disable",
                ["Security:AllowInsecureTransport"] = "true",
            });

        Assert.NotNull(provider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value.Vantigo);
    }

    [Fact]
    public void Development_KeepsWorkingWithoutTls()
    {
        ServiceProvider provider = Build(
            Environments.Development,
            new Dictionary<string, string?> { ["ConnectionStrings:vantigo"] = "Host=localhost;Database=vantigo" });

        Assert.NotNull(provider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value.Vantigo);
    }

    [Fact]
    public void Production_AlsoCoversTheDataProtectionConnectionStringOverride()
    {
        ServiceProvider provider = Build(
            Environments.Production,
            new Dictionary<string, string?>
            {
                ["ConnectionStrings:vantigo"] = "Host=db;Database=vantigo;SSL Mode=VerifyFull",
                ["DataProtection:PostgreSql:ConnectionString"] = "Host=db;Database=keys;SSL Mode=Prefer",
            });

        OptionsValidationException exception = Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<DataProtectionPostgreSqlOptions>>().Value);

        Assert.Contains("DataProtection:PostgreSql:ConnectionString", exception.Message, StringComparison.Ordinal);
    }

    private static ServiceProvider Build(string environmentName, Dictionary<string, string?> values)
    {
        IConfigurationRoot configuration = new ConfigurationBuilder().AddInMemoryCollection(values).Build();
        ServiceCollection services = new();
        services.AddSingleton<IHostEnvironment>(new TransportTestHostEnvironment(environmentName));
        services.AddTransportSecurityOptions(configuration);
        services.AddConnectionStringsOptions(configuration);
        services.AddDataProtectionPostgreSqlOptions(configuration);
        return services.BuildServiceProvider();
    }
}

public sealed class PublicOriginTransportValidationTests
{
    [Theory]
    [InlineData("Production")]
    [InlineData("Staging")]
    public void AnHttpOrigin_OutsideDevelopment_FailsStartupValidation(string environmentName)
    {
        ServiceProvider provider = Build(
            environmentName,
            new Dictionary<string, string?> { ["App:PublicOrigin"] = "http://vantigo.example.com" });

        OptionsValidationException exception = Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<AppPublicOriginOptions>>().Value);

        Assert.Contains("App:PublicOrigin", exception.Message, StringComparison.Ordinal);
        Assert.Contains("must use https", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void AnHttpOrigin_WithTheEscapeHatch_Starts()
    {
        ServiceProvider provider = Build(
            Environments.Production,
            new Dictionary<string, string?>
            {
                ["App:PublicOrigin"] = "http://localhost:8080",
                ["Security:AllowInsecureTransport"] = "true",
            });

        Assert.Equal(
            "http://localhost:8080",
            provider.GetRequiredService<IOptions<AppPublicOriginOptions>>().Value.Normalized);
    }

    [Fact]
    public void AnHttpsOrigin_InProduction_Starts()
    {
        ServiceProvider provider = Build(
            Environments.Production,
            new Dictionary<string, string?> { ["App:PublicOrigin"] = "https://vantigo.example.com" });

        Assert.Equal(
            "https://vantigo.example.com",
            provider.GetRequiredService<IOptions<AppPublicOriginOptions>>().Value.Normalized);
    }

    [Fact]
    public void AnHttpOrigin_InDevelopment_Starts()
    {
        ServiceProvider provider = Build(
            Environments.Development,
            new Dictionary<string, string?> { ["App:PublicOrigin"] = "http://localhost:10011" });

        Assert.Equal(
            "http://localhost:10011",
            provider.GetRequiredService<IOptions<AppPublicOriginOptions>>().Value.Normalized);
    }

    [Fact]
    public void AnInvalidOrigin_IsStillReportedThroughOptionsValidation()
    {
        ServiceProvider provider = Build(
            Environments.Production,
            new Dictionary<string, string?> { ["App:PublicOrigin"] = "https://vantigo.example.com/with-a-path" });

        OptionsValidationException exception = Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<AppPublicOriginOptions>>().Value);

        Assert.Contains("App:PublicOrigin", exception.Message, StringComparison.Ordinal);
    }

    private static ServiceProvider Build(string environmentName, Dictionary<string, string?> values)
    {
        IConfigurationRoot configuration = new ConfigurationBuilder().AddInMemoryCollection(values).Build();
        ServiceCollection services = new();
        services.AddSingleton<IHostEnvironment>(new TransportTestHostEnvironment(environmentName));
        services.AddTransportSecurityOptions(configuration);
        services.AddAppPublicOriginOptions(configuration);
        return services.BuildServiceProvider();
    }
}

public sealed class SmtpTransportValidationTests
{
    [Fact]
    public void PlaintextSmtp_InProduction_FailsStartupValidation()
    {
        OptionsValidationException exception = Assert.Throws<OptionsValidationException>(
            () => Resolve(Environments.Production, new Dictionary<string, string?>
            {
                ["Email:Provider"] = "Smtp",
                ["Email:Smtp:Host"] = "smtp.example.com",
                ["Email:Smtp:Port"] = "25",
                ["Email:Smtp:EnableSsl"] = "false",
            }));

        Assert.Contains("Email:Smtp must negotiate TLS", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void PlaintextSmtp_InProduction_NeedsBothOptIns()
    {
        OptionsValidationException exception = Assert.Throws<OptionsValidationException>(
            () => Resolve(Environments.Production, new Dictionary<string, string?>
            {
                ["Email:Provider"] = "Smtp",
                ["Email:Smtp:Host"] = "smtp.example.com",
                ["Email:Smtp:Port"] = "25",
                ["Email:Smtp:EnableSsl"] = "false",
                ["Email:Smtp:AllowInsecurePlaintext"] = "true",
            }));

        Assert.Contains("AllowInsecureTransport", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void PlaintextSmtp_WithBothOptIns_Starts()
    {
        EmailOptions options = Resolve(Environments.Production, new Dictionary<string, string?>
        {
            ["Email:Provider"] = "Smtp",
            ["Email:Smtp:Host"] = "smtp.example.com",
            ["Email:Smtp:Port"] = "25",
            ["Email:Smtp:EnableSsl"] = "false",
            ["Email:Smtp:AllowInsecurePlaintext"] = "true",
            ["Security:AllowInsecureTransport"] = "true",
        });

        Assert.False(options.Smtp.UsesTls);
    }

    [Fact]
    public void ImplicitTlsPort_CountsAsTls()
    {
        EmailOptions options = Resolve(Environments.Production, new Dictionary<string, string?>
        {
            ["Email:Provider"] = "Smtp",
            ["Email:Smtp:Host"] = "smtp.example.com",
            ["Email:Smtp:Port"] = "465",
            ["Email:Smtp:EnableSsl"] = "false",
        });

        Assert.True(options.Smtp.UsesTls);
    }

    [Fact]
    public void PlaintextSmtp_InDevelopment_OnlyNeedsTheEmailOptIn()
    {
        EmailOptions options = Resolve(Environments.Development, new Dictionary<string, string?>
        {
            ["Email:Provider"] = "Smtp",
            ["Email:Smtp:Host"] = "localhost",
            ["Email:Smtp:Port"] = "1025",
            ["Email:Smtp:EnableSsl"] = "false",
            ["Email:Smtp:AllowInsecurePlaintext"] = "true",
        });

        Assert.False(options.Smtp.UsesTls);
    }

    private static EmailOptions Resolve(string environmentName, Dictionary<string, string?> values)
    {
        IConfigurationRoot configuration = new ConfigurationBuilder().AddInMemoryCollection(values).Build();
        ServiceCollection services = new();
        services.AddSingleton<IHostEnvironment>(new TransportTestHostEnvironment(environmentName));
        services.AddTransportSecurityOptions(configuration);
        services.AddEmailOptions(configuration);
        using ServiceProvider provider = services.BuildServiceProvider();
        return provider.GetRequiredService<IOptions<EmailOptions>>().Value;
    }
}

internal sealed class TransportTestHostEnvironment(string environmentName) : IHostEnvironment
{
    public string EnvironmentName { get; set; } = environmentName;
    public string ApplicationName { get; set; } = "Vantigo.Configuration.Tests";
    public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
    public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
}