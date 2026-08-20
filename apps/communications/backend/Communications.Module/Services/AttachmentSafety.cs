using Microsoft.Net.Http.Headers;

namespace Vantigo.Communications.Services;

internal static class AttachmentSafety
{
    internal static string SafeFileName(string? value)
    {
        var name = Path.GetFileName(string.IsNullOrWhiteSpace(value) ? "attachment" : value).Trim();
        var filtered = new string(name.Where(character => !char.IsControl(character) && character != '/' && character != '\\').ToArray());
        return string.IsNullOrWhiteSpace(filtered) ? "attachment" : filtered[..Math.Min(filtered.Length, 200)];
    }

    internal static string ContentType(string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Length > 200 || value.Any(char.IsControl)) return "application/octet-stream";
        try { return MediaTypeHeaderValue.Parse(value).MediaType.Value ?? "application/octet-stream"; }
        catch (FormatException) { return "application/octet-stream"; }
    }

    internal static string? ContentId(string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Length > 500 || value.Any(char.IsControl)) return null;
        var normalized = value.Trim().Trim('<', '>');
        return normalized.Length == 0 || normalized.Any(char.IsWhiteSpace) ? null : normalized;
    }
}