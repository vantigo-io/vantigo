using Microsoft.Extensions.Configuration;

namespace Vantigo.Hosting;

/// <summary>
/// Whitelabeling settings read from the <c>App:*</c> configuration section and
/// injected into the SPA entry document at serve time
/// (<c>window.__VANTIGO_APP__</c>).
/// </summary>
public sealed record AppBranding(
    string Title,
    string? LogoUrl,
    string? SupportEmail,
    string? SupportPhone,
    string? SupportUrl)
{
    /// <summary>
    /// Reads the branding settings, falling back to
    /// <paramref name="defaultTitle"/> (the app's name) for the title.
    /// </summary>
    public static AppBranding Load(IConfiguration configuration, string defaultTitle)
    {
        static string? Optional(string? value) =>
            string.IsNullOrWhiteSpace(value) ? null : value.Trim();

        return new AppBranding(
            Title: Optional(configuration["App:Title"]) ?? defaultTitle,
            LogoUrl: Optional(configuration["App:LogoUrl"]),
            SupportEmail: Optional(configuration["App:Support:Email"]),
            SupportPhone: Optional(configuration["App:Support:Phone"]),
            SupportUrl: Optional(configuration["App:Support:Url"]));
    }
}