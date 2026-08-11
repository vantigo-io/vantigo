using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Services;

public sealed class InvitationTokenServiceUrlTests
{
    private static AppPublicUrls Urls(Dictionary<string, string?> values)
    {
        var origin = new AppPublicOriginOptions();
        if (values.TryGetValue("App:PublicOrigin", out var publicOrigin) && publicOrigin is not null)
        {
            origin.PublicOrigin = publicOrigin;
        }

        var basePath = new AppBasePathOptions();
        if (values.TryGetValue("App:BasePath", out var basePathValue) && basePathValue is not null)
        {
            basePath.BasePath = basePathValue;
        }

        return new AppPublicUrls(Options.Create(origin), Options.Create(basePath));
    }

    private static IConfiguration CreateConfiguration(Dictionary<string, string?> values) =>
        new ConfigurationBuilder().AddInMemoryCollection(values).Build();

    [Fact]
    public void InvitationUrl_WithoutConfiguration_UsesTheDevelopmentFallback()
    {
        var url = InvitationTokenService.InvitationUrl(new InvitationOptions(), Urls([]), "raw-token");

        Assert.Equal("http://localhost:5173/invitations/accept?token=raw-token", url);
    }

    [Fact]
    public void InvitationUrl_DerivesFromThePublicOriginAndBasePath()
    {
        var url = InvitationTokenService.InvitationUrl(
            new InvitationOptions(),
            Urls(new Dictionary<string, string?>
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
            new InvitationOptions { AcceptUrl = "https://other.example.com/join?token={token}" },
            Urls(new Dictionary<string, string?>
            {
                ["App:PublicOrigin"] = "https://vantigo.example.com",
                ["App:BasePath"] = "/customers",
            }),
            "raw-token");

        Assert.Equal("https://other.example.com/join?token=raw-token", url);
    }

    [Fact]
    public void InvitationUrl_UrlEncodesTheToken()
    {
        var url = InvitationTokenService.InvitationUrl(
            new InvitationOptions(),
            Urls(new Dictionary<string, string?> { ["App:PublicOrigin"] = "https://vantigo.example.com" }),
            "a+b/c");

        Assert.Equal("https://vantigo.example.com/invitations/accept?token=a%2Bb%2Fc", url);
    }

    [Fact]
    public void PasswordResetUrl_DerivesFromThePublicOriginAndBasePath()
    {
        var url = InvitationTokenService.PasswordResetUrl(
            new PasswordResetOptions(),
            Urls(new Dictionary<string, string?>
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
            new PasswordResetOptions { ResetUrl = "https://other.example.com/reset?email={email}&token={token}" },
            Urls(new Dictionary<string, string?>
            {
                ["App:PublicOrigin"] = "https://vantigo.example.com",
            }),
            "user@example.test",
            "encoded-token");

        Assert.StartsWith("https://other.example.com/reset?", url);
    }

    [Fact]
    public void PasswordResetUrl_WithoutConfiguration_UsesTheDevelopmentFallback()
    {
        var url = InvitationTokenService.PasswordResetUrl(
            new PasswordResetOptions(),
            Urls([]),
            "user@example.test",
            "token");

        Assert.StartsWith("http://localhost:5173/password-reset?", url);
    }

    [Fact]
    public void Urls_FailFastOnAnInvalidPublicOrigin()
    {
        Assert.Throws<InvalidOperationException>(() => Urls(new Dictionary<string, string?>
        {
            ["App:PublicOrigin"] = "vantigo.example.com",
        }));
    }
}