using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Services;

public sealed class ApplicationEmailSenderTests
{
    private const string SecretLink = "https://example.test/reset?token=temporary-secret";

    [Fact]
    public async Task LoggingSender_NeverLogsSubjectOrBody()
    {
        var logger = new RecordingLogger();
        var sender = new LoggingApplicationEmailSender(logger);
        const string body = $"Use {SecretLink} to continue.";

        await sender.SendAsync(new ApplicationEmail(
            "person@example.test",
            "Reset your Vantigo password",
            body));

        var state = Assert.Single(logger.States);
        var values = Assert.IsAssignableFrom<IReadOnlyList<KeyValuePair<string, object?>>>(state);
        Assert.Equal("person@example.test", Value(values, "Recipient"));
        Assert.NotNull(Value(values, "MessageId"));
        Assert.DoesNotContain(values, item => item.Key is "Subject" or "Body");
    }

    [Fact]
    public async Task LoggingSender_FormattedLogDoesNotContainSecretOrLink()
    {
        var logger = new RecordingLogger();
        var sender = new LoggingApplicationEmailSender(logger);
        const string body = $"Use {SecretLink} to continue.";

        await sender.SendAsync(new ApplicationEmail(
            "person@example.test",
            "Reset your Vantigo password",
            body));

        Assert.DoesNotContain(SecretLink, logger.FormattedMessage, StringComparison.Ordinal);
        Assert.DoesNotContain("token=", logger.FormattedMessage, StringComparison.Ordinal);
        Assert.DoesNotContain(body, logger.FormattedMessage, StringComparison.Ordinal);
    }

    [Fact]
    public void AddApplicationEmail_SelectsSmtpSender_WhenProviderIsSmtp()
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.Configure<EmailOptions>(options =>
        {
            options.Provider = "Smtp";
            options.Smtp.Host = "smtp.example.test";
        });
        services.AddApplicationEmail();

        using var provider = services.BuildServiceProvider();

        Assert.IsType<SmtpApplicationEmailSender>(provider.GetRequiredService<IApplicationEmailSender>());
    }

    [Fact]
    public void AddApplicationEmail_SelectsLoggingSender_WhenProviderIsLogging()
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.Configure<EmailOptions>(options => options.Provider = "Logging");
        services.AddApplicationEmail();

        using var provider = services.BuildServiceProvider();

        Assert.IsType<LoggingApplicationEmailSender>(provider.GetRequiredService<IApplicationEmailSender>());
    }

    private static object? Value(IReadOnlyList<KeyValuePair<string, object?>> state, string key) =>
        state.Single(item => item.Key == key).Value;

    private sealed class RecordingLogger : ILogger<LoggingApplicationEmailSender>
    {
        public List<object> States { get; } = [];
        public string FormattedMessage { get; private set; } = string.Empty;

        public IDisposable? BeginScope<TState>(TState state) where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => true;

        public void Log<TState>(
            LogLevel logLevel,
            EventId eventId,
            TState state,
            Exception? exception,
            Func<TState, Exception?, string> formatter)
        {
            States.Add(state!);
            FormattedMessage = formatter(state, exception);
        }
    }
}