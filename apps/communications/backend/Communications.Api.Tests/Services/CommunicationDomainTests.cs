using Vantigo.Communications.Api.Database.Communications;
using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Tests.Services;

public sealed class CommunicationDomainTests
{
    [Fact]
    public void External_links_are_opaque_and_preserve_string_ids()
    {
        var message = new EmailMessage { Id = Guid.NewGuid(), MailboxId = Guid.NewGuid(), Subject = "subject" };
        message.ExternalLinks.Add(new ExternalEntityLink
        {
            Id = Guid.NewGuid(),
            MessageId = message.Id,
            SourceSystem = "customers",
            SourceInstance = "tenant-a",
            EntityType = "account",
            ExternalEntityId = "000123",
            DisplayLabel = "Example",
        });

        var link = Assert.Single(message.ExternalLinks);
        Assert.Equal("000123", link.ExternalEntityId);
        Assert.Equal("tenant-a", link.SourceInstance);
    }

    [Fact]
    public void Suppression_normalization_is_case_insensitive()
    {
        Assert.Equal("PERSON@EXAMPLE.TEST", EmailSuppression.Normalize(" person@example.test "));
    }

    [Fact]
    public void Envelope_uses_delivery_types_without_tracking_headers()
    {
        var mailbox = new SharedMailbox { Id = Guid.NewGuid(), FromAddress = "noreply@example.test" };
        var message = new EmailMessage { Id = Guid.NewGuid(), MailboxId = mailbox.Id, Subject = "subject" };
        message.Deliveries.Add(new RecipientDelivery { Id = Guid.NewGuid(), MessageId = message.Id, EmailAddress = "to@example.test", RecipientType = "to" });
        message.Deliveries.Add(new RecipientDelivery { Id = Guid.NewGuid(), MessageId = message.Id, EmailAddress = "blind@example.test", RecipientType = "bcc" });

        var envelope = EmailEnvelopeFactory.Create(message, mailbox);

        Assert.Equal(["to@example.test"], envelope.To);
        Assert.Equal(["blind@example.test"], envelope.Bcc);
        Assert.Empty(envelope.Cc);
    }
}