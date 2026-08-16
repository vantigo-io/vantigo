using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Communications.Services;
using Vantigo.Configuration;
using Vantigo.Storage.Abstractions;

namespace Vantigo.Communications.Module.Tests.Services;

public sealed class EmailSenderTests
{
    [Fact]
    public void Envelope_has_stable_message_id()
    {
        var channel = new Channel { Id = Guid.NewGuid(), Type = "email", Address = "sender@example.test" };
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "subject" };
        var message = new ConversationMessage { Id = Guid.Parse("11111111-1111-1111-1111-111111111111"), ConversationId = conversation.Id, Direction = "outbound", Subject = "subject" };

        var envelope = EmailEnvelopeFactory.Create(message, conversation, channel);

        Assert.Equal(message.Id, envelope.MessageId);
    }

    [Fact]
    public async Task Invalid_timeout_is_rejected_before_network_connect()
    {
        var configuration = new ConfigurationBuilder().AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Smtp:Host"] = "smtp.example.test",
            ["Smtp:TimeoutSeconds"] = "60",
            ["Outbox:LeaseSeconds"] = "60",
        }).Build();
        var environment = new TestHostEnvironment { EnvironmentName = Environments.Production };
        var smtpOptions = Options.Create(configuration.GetSection("Smtp").Get<SmtpOptions>() ?? new());
        var outboxOptions = Options.Create(configuration.GetSection("Outbox").Get<OutboxOptions>() ?? new());
        var provider = new SmtpDeliveryProvider(smtpOptions, outboxOptions, environment, new MailboxCredentialProtector(new Microsoft.AspNetCore.DataProtection.EphemeralDataProtectionProvider()), new EmptyObjectStore());
        var envelope = new EmailEnvelope(Guid.NewGuid(), "sender@example.test", null, "subject", "body", null, ["recipient@example.test"], [], []);

        await Assert.ThrowsAsync<InvalidOperationException>(() => provider.SendAsync(envelope, null, CancellationToken.None));
    }

    private sealed class TestHostEnvironment : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = string.Empty;
        public string ApplicationName { get; set; } = "tests";
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public Microsoft.Extensions.FileProviders.IFileProvider ContentRootFileProvider { get; set; } = new Microsoft.Extensions.FileProviders.NullFileProvider();
    }

    private sealed class EmptyObjectStore : IObjectStore<CommunicationsStorageScope>
    {
        public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default) => Task.CompletedTask;
        public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) => Task.FromResult<Stream?>(null);
        public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) => Task.FromResult(false);
        public Task DeleteAsync(string key, CancellationToken cancellationToken = default) => Task.CompletedTask;
    }
}