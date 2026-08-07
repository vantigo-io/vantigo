using Vantigo.Communications.Api.Endpoints;

namespace Vantigo.Communications.Api.Tests.Endpoints;

public sealed class MailboxValidationTests
{
    [Fact]
    public void Mailgun_create_requires_domain_region_and_api_key()
    {
        var errors = CommunicationValidation.ValidateMailbox(new CreateMailboxRequest(
            "mailgun@example.test", null, "mailgun", null,
            null, new MailgunMailboxCredentialRequest(null, "ap", null)));

        Assert.Contains("mailgun", errors.Keys);
    }

    [Theory]
    [InlineData(0)]
    [InlineData(65536)]
    public void Smtp_create_rejects_ports_outside_the_valid_range(int port)
    {
        var errors = CommunicationValidation.ValidateMailbox(new CreateMailboxRequest(
            "smtp@example.test", null, "smtp", null,
            new SmtpMailboxCredentialRequest("smtp.example.test", port, false, null, "password"), null));

        Assert.Contains("smtp", errors.Keys);
    }

    [Fact]
    public void Unknown_provider_is_rejected_on_create_and_update()
    {
        var createErrors = CommunicationValidation.ValidateMailbox(new CreateMailboxRequest(
            "unknown@example.test", null, "sendgrid"));
        var updateErrors = CommunicationValidation.ValidateMailboxUpdate(new UpdateMailboxRequest(
            null, null, null, "sendgrid"));

        Assert.Contains("provider", createErrors.Keys);
        Assert.Contains("provider", updateErrors.Keys);
    }

    [Fact]
    public void Mailgun_update_requires_complete_credentials_when_provider_is_selected()
    {
        var errors = CommunicationValidation.ValidateMailboxUpdate(new UpdateMailboxRequest(
            null, null, null, "mailgun", null, new MailgunMailboxCredentialRequest("domain.test", "us", null)));

        Assert.Contains("mailgun", errors.Keys);
    }
}