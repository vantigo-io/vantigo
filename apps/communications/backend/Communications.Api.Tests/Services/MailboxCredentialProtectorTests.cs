using Microsoft.AspNetCore.DataProtection;

using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Tests.Services;

public sealed class MailboxCredentialProtectorTests
{
    [Fact]
    public void Protect_and_unprotect_round_trip_the_credential()
    {
        var protector = new MailboxCredentialProtector(new EphemeralDataProtectionProvider());

        var ciphertext = protector.Protect("secret-api-key");

        Assert.NotEqual("secret-api-key", ciphertext);
        Assert.Equal("secret-api-key", protector.Unprotect(ciphertext));
    }
}