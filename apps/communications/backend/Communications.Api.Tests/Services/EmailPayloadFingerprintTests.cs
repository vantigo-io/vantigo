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
}
