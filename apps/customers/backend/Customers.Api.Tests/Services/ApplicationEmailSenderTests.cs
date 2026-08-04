using Microsoft.Extensions.Logging;

using Vantigo.Customers.Api.Services;

namespace Vantigo.Customers.Api.Tests.Services;

public sealed class ApplicationEmailSenderTests
{
    [Fact]
    public async Task LoggingSender_EmitsRecipientSubjectAndFullBodyAsStructuredState()
    {
        var logger = new RecordingLogger();
        var sender = new LoggingApplicationEmailSender(logger);
        const string body = "Use https://example.test/reset?token=temporary-secret to continue.";

        await sender.SendAsync(new ApplicationEmail(
            "person@example.test",
            "Reset your Vantigo password",
            body));

        var state = Assert.Single(logger.States);
        var values = Assert.IsAssignableFrom<IReadOnlyList<KeyValuePair<string, object?>>>(state);
        Assert.Equal("person@example.test", Value(values, "Recipient"));
        Assert.Equal("Reset your Vantigo password", Value(values, "Subject"));
        Assert.Equal(body, Value(values, "Body"));
    }

    private static object? Value(IReadOnlyList<KeyValuePair<string, object?>> state, string key) =>
        state.Single(item => item.Key == key).Value;

    private sealed class RecordingLogger : ILogger<LoggingApplicationEmailSender>
    {
        public List<object> States { get; } = [];

        public IDisposable? BeginScope<TState>(TState state) where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => true;

        public void Log<TState>(
            LogLevel logLevel,
            EventId eventId,
            TState state,
            Exception? exception,
            Func<TState, Exception?, string> formatter)
            where TState : notnull => States.Add(state!);
    }
}
