using Vantigo.Communications.Endpoints;

namespace Vantigo.Communications.Module.Tests.Endpoints;

public sealed class CommunicationValidationTests
{
    [Fact]
    public void Conversation_requires_to_recipient_and_body()
    {
        var errors = CommunicationValidation.ValidateConversation(new CreateConversationRequest(null, [], null, "subject", null, null));
        Assert.Contains("body", errors.Keys); Assert.Contains("to", errors.Keys);
    }

    [Fact]
    public void Rejects_duplicate_recipient_across_recipient_types()
    {
        var errors = CommunicationValidation.ValidateConversation(new CreateConversationRequest(null, [], [new ChannelRecipientRequest(null, "PERSON@example.test")], "subject", "body", null, [new EmailRecipientRequest("person@example.test")], [new EmailRecipientRequest("PERSON@example.test")]));
        Assert.Contains("recipients", errors.Keys);
    }

    [Fact]
    public void Mailgun_create_requires_inbound_signing_key()
    {
        var errors = CommunicationValidation.ValidateChannel(new CreateChannelRequest(
            "email", "inbound@example.test", null, "mailgun", false, null,
            new MailgunChannelCredentialRequest("example.test", "us", "key", null)));

        Assert.Contains("mailgun", errors.Keys);
    }

    [Fact]
    public void Mailgun_update_allows_omitted_secrets()
    {
        var errors = CommunicationValidation.ValidateChannelUpdate(new UpdateChannelRequest(
            null, null, null, "mailgun", null,
            new MailgunChannelCredentialRequest("example.test", "us", null, null)));

        Assert.DoesNotContain("mailgun", errors.Keys);
    }
}