using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Tests.Services;

public sealed class EmailPayloadFingerprintTests
{
    [Fact]
    public void Same_payload_has_same_fingerprint_and_changes_are_detected()
    {
        var first = new { subject = "hello", body = "message", recipients = new[] { "a@example.test" } };
        var same = new { subject = "hello", body = "message", recipients = new[] { "a@example.test" } };
        var changed = new { subject = "changed", body = "message", recipients = new[] { "a@example.test" } };

        Assert.Equal(EmailPayloadFingerprint.Create(first), EmailPayloadFingerprint.Create(same));
        Assert.NotEqual(EmailPayloadFingerprint.Create(first), EmailPayloadFingerprint.Create(changed));
    }

    [Fact]
    public void Mailbox_id_is_part_of_the_fingerprint()
    {
        var first = new { subject = "hello", body = "message", mailboxId = Guid.Parse("11111111-1111-1111-1111-111111111111") };
        var changedMailbox = new { subject = "hello", body = "message", mailboxId = Guid.Parse("22222222-2222-2222-2222-222222222222") };

        Assert.NotEqual(EmailPayloadFingerprint.Create(first), EmailPayloadFingerprint.Create(changedMailbox));
    }
}