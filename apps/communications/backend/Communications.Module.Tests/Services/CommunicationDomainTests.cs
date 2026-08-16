using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;

namespace Vantigo.Communications.Module.Tests.Services;

public sealed class CommunicationDomainTests
{
    [Fact]
    public void Participants_preserve_normalized_channel_addresses()
    {
        var participant = new Participant
        {
            Id = Guid.NewGuid(),
            ChannelId = Guid.NewGuid(),
            Address = "PERSON@EXAMPLE.TEST",
            DisplayName = "Example",
        };

        Assert.Equal("PERSON@EXAMPLE.TEST", participant.Address);
    }

    [Fact]
    public void Suppression_normalization_is_case_insensitive()
    {
        Assert.Equal("PERSON@EXAMPLE.TEST", EmailSuppression.Normalize(" person@example.test "));
    }

    [Fact]
    public void Envelope_uses_delivery_types_without_tracking_headers()
    {
        var channel = new Channel { Id = Guid.NewGuid(), Type = "email", Address = "noreply@example.test" };
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = "subject" };
        var message = new ConversationMessage { Id = Guid.NewGuid(), ConversationId = conversation.Id, Direction = "outbound", Subject = "subject" };
        message.Deliveries.Add(new MessageDelivery { Id = Guid.NewGuid(), MessageId = message.Id, RecipientAddress = "to@example.test", RecipientType = "to" });
        message.Deliveries.Add(new MessageDelivery { Id = Guid.NewGuid(), MessageId = message.Id, RecipientAddress = "blind@example.test", RecipientType = "bcc" });

        var envelope = EmailEnvelopeFactory.Create(message, conversation, channel);

        Assert.Equal(["to@example.test"], envelope.To);
        Assert.Equal(["blind@example.test"], envelope.Bcc);
        Assert.Empty(envelope.Cc);
    }
}