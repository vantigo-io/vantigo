using Ganss.Xss;

namespace Vantigo.Communications.Services;

internal static class InboundHtmlSanitizer
{
    internal static string? Sanitize(string? html, int maxCharacters)
    {
        if (string.IsNullOrWhiteSpace(html)) return null;
        var sanitizer = new HtmlSanitizer();
        foreach (var tag in new[] { "script", "style", "form", "input", "button", "textarea", "select", "option", "iframe", "frame", "frameset", "object", "embed", "svg", "math", "meta", "base", "link", "img" })
            sanitizer.AllowedTags.Remove(tag);
        sanitizer.AllowedAttributes.Remove("style");
        sanitizer.AllowedAttributes.Remove("src");
        sanitizer.AllowedAttributes.Remove("srcset");
        sanitizer.AllowedSchemes.Clear();
        sanitizer.AllowedSchemes.Add("http");
        sanitizer.AllowedSchemes.Add("https");
        sanitizer.AllowedSchemes.Add("mailto");
        var clean = sanitizer.Sanitize(html);
        return clean.Length <= maxCharacters ? clean : clean[..maxCharacters];
    }
}