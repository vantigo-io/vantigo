using Microsoft.Extensions.Configuration;

using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Services;

public sealed class InvitationTokenServiceUrlTests
{
    private static IConfiguration Config(Dictionary<string, string?> values) =>
        new ConfigurationBuilder().AddInMemoryCollection(values).Build();

    [Fact]
    public void InvitationUrl_WithoutConfiguration_UsesTheDevelopmentFallback()
    {
        var url = InvitationTokenService.InvitationUrl(Config([]), "raw-token");

        Assert.Equal("http://localhost:5173/invitations/accept?token=raw-token", url);
    }

    [Fact]
    public void InvitationUrl_DerivesFromThePublicOriginAndBasePath()
    {
        var url = InvitationTokenService.InvitationUrl(
            Config(new Dictionary<string, string?>
            {
                ["App:PublicOrigin"] = "https://vantigo.example.com",
                ["App:BasePath"] = "/customers",
            }),
            "raw-token");

        Assert.Equal("https://vantigo.example.com/customers/invitations/accept?token=raw-token", url);
    }

    [Fact]
    public void InvitationUrl_ExplicitTemplateWinsOverThePublicOrigin()
    {
        var url = InvitationTokenService.InvitationUrl(
            Config(new Dictionary<string, string?>
            {
                ["App:PublicOrigin"] = "https://vantigo.example.com",
                ["App:BasePath"] = "/customers",
                ["Authentication:Invitations:AcceptUrl"] = "https://other.example.com/join?token={token}",
            }),
            "raw-token");

        Assert.Equal("https://other.example.com/join?token=raw-token", url);
    }

    [Fact]
    public void InvitationUrl_UrlEncodesTheToken()
    {
        var url = InvitationTokenService.InvitationUrl(
            Config(new Dictionary<string, string?> { ["App:PublicOrigin"] = "https://vantigo.example.com" }),
            "a+b/c");

        Assert.Equal("https://vantigo.example.com/invitations/accept?token=a%2Bb%2Fc", url);
    }

    [Fact]
    public void PasswordResetUrl_DerivesFromThePublicOriginAndBasePath()
    {
        var url = InvitationTokenService.PasswordResetUrl(
            Config(new Dictionary<string, string?>
            {
                ["App:PublicOrigin"] = "https://vantigo.example.com",
                ["App:BasePath"] = "/customers",
            }),
            "user@example.test",
            "encoded-token");

        Assert.Equal(
            "https://vantigo.example.com/customers/password-reset?email=user%40example.test&token=encoded-token",
            url);
    }

    [Fact]
    public void PasswordResetUrl_ExplicitTemplateWinsOverThePublicOrigin()
    {
        var url = InvitationTokenService.PasswordResetUrl(
            Config(new Dictionary<string, string?>
            {
                ["App:PublicOrigin"] = "https://vantigo.example.com",
                ["Authentication:PasswordReset:ResetUrl"] = "https://other.example.com/reset?email={email}&token={token}",
            }),
            "user@example.test",
            "encoded-token");

        Assert.StartsWith("https://other.example.com/reset?", url);
    }

    [Fact]
    public void PasswordResetUrl_WithoutConfiguration_UsesTheDevelopmentFallback()
    {
        var url = InvitationTokenService.PasswordResetUrl(Config([]), "user@example.test", "token");

        Assert.StartsWith("http://localhost:5173/password-reset?", url);
    }

    [Fact]
    public void Urls_FailFastOnAnInvalidPublicOrigin()
    {
        var configuration = Config(new Dictionary<string, string?>
        {
            ["App:PublicOrigin"] = "vantigo.example.com",
        });

        Assert.Throws<InvalidOperationException>(() => InvitationTokenService.InvitationUrl(configuration, "t"));
    }
}