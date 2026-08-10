using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Hosting;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;

namespace Vantigo.Communications.Module.Tests.Services;

public sealed class EmailSenderTests
{
    [Fact]
    public void Envelope_has_stable_message_id()
    {
        var message = new EmailMessage { Id = Guid.Parse("11111111-1111-1111-1111-111111111111"), MailboxId = Guid.NewGuid(), Subject = "subject" };
        var mailbox = new SharedMailbox { Id = message.MailboxId, FromAddress = "sender@example.test" };

        var envelope = EmailEnvelopeFactory.Create(message, mailbox);

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
        var provider = new SmtpDeliveryProvider(configuration, environment, new MailboxCredentialProtector(new Microsoft.AspNetCore.DataProtection.EphemeralDataProtectionProvider()));
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
}