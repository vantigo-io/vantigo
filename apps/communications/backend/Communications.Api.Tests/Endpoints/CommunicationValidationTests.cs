using Vantigo.Communications.Api.Endpoints;

namespace Vantigo.Communications.Api.Tests.Endpoints;

public sealed class CommunicationValidationTests
{
    [Fact]
    public void Requires_to_recipient_and_message_body()
    {
        var errors = CommunicationValidation.Validate(new CreateEmailRequest("subject", null, null, [], null, null, null, null));

        Assert.Contains("body", errors.Keys);
        Assert.Contains("to", errors.Keys);
    }

    [Fact]
    public void Rejects_duplicate_recipient_across_recipient_types()
    {
        var errors = CommunicationValidation.Validate(new CreateEmailRequest(
            "subject", "body", null,
            [new EmailRecipientRequest("person@example.test")],
            [new EmailRecipientRequest("PERSON@example.test")], null, null, null));

        Assert.Contains("recipients", errors.Keys);
    }
}