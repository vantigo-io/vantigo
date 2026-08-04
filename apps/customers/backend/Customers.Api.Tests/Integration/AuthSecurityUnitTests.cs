using Vantigo.Customers.Api.Services;

namespace Vantigo.Customers.Api.Tests.Integration;

public sealed class AuthSecurityUnitTests
{
    [Fact]
    public void InvitationTokensAreOpaqueAndOnlyTheirHashIsStable()
    {
        var first = InvitationTokenService.Create();
        var second = InvitationTokenService.Create();

        Assert.NotEqual(first.RawToken, second.RawToken);
        Assert.NotEqual(first.Hash, second.Hash);
        Assert.Equal(first.Hash, InvitationTokenService.Hash(first.RawToken));
        Assert.Equal(string.Empty, InvitationTokenService.Hash("not-a-valid-token"));
    }
}
