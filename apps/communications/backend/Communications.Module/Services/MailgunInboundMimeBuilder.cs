using System.Text.Json;

using MailKit;

using MimeKit;

namespace Vantigo.Communications.Services;

internal static class MailgunInboundMimeBuilder
{
    internal static byte[] CreateSynthetic(MailgunInboundForm form, DateTimeOffset now)
    {
        var message = new MimeMessage();
        AddHeaders(message, form.Headers);
        AddMailboxes(message.Cc, form.Cc);
        if (ParseHeaders(form.Headers).TryGetValue("Cc", out var headerCc)) AddMailboxes(message.Cc, headerCc);
        if (message.From.Count == 0 && TryMailbox(form.From ?? form.Sender, out var from)) message.From.Add(from!);
        if (message.To.Count == 0 && TryMailbox(form.Recipient, out var to)) message.To.Add(to!);
        if (message.Subject is null)
        {
            var subject = Clean(form.Subject, 998);
            if (subject is not null) message.Subject = subject;
        }
        if (message.Date == DateTimeOffset.MinValue) message.Date = now;

        var builder = new BodyBuilder
        {
            TextBody = form.Text,
            HtmlBody = form.Html,
        };
        foreach (var attachment in form.Attachments)
        {
            var item = builder.Attachments.Add(attachment.FileName, attachment.Bytes, ParseContentType(attachment.ContentType));
            item.ContentDisposition ??= new ContentDisposition(ContentDisposition.Attachment);
        }
        message.Body = builder.ToMessageBody();
        using var stream = new MemoryStream();
        message.WriteTo(stream);
        return stream.ToArray();
    }

    internal static IReadOnlyDictionary<string, string> ParseHeaders(string? encoded)
    {
        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        if (string.IsNullOrWhiteSpace(encoded) || encoded.Length > 512 * 1024) return headers;
        try
        {
            using var document = JsonDocument.Parse(encoded);
            if (document.RootElement.ValueKind == JsonValueKind.Object)
            {
                foreach (var property in document.RootElement.EnumerateObject()) headers[property.Name] = property.Value.ToString();
            }
            else if (document.RootElement.ValueKind == JsonValueKind.Array)
            {
                foreach (var item in document.RootElement.EnumerateArray())
                {
                    if (item.ValueKind == JsonValueKind.Array && item.GetArrayLength() >= 2 && item[0].GetString() is { } pairName)
                        headers[pairName] = item[1].GetString() ?? string.Empty;
                    else if (item.ValueKind == JsonValueKind.Object && item.TryGetProperty("name", out var name) && item.TryGetProperty("value", out var value) && name.GetString() is { } objectName)
                        headers[objectName] = value.GetString() ?? string.Empty;
                }
            }
        }
        catch (JsonException) { }
        return headers;
    }

    private static void AddHeaders(MimeMessage message, string? encoded)
    {
        foreach (var header in ParseHeaders(encoded))
        {
            AddHeader(message, header.Key, header.Value);
        }
    }

    private static void AddHeader(MimeMessage message, string? name, string? value)
    {
        if (string.IsNullOrWhiteSpace(name) || string.IsNullOrWhiteSpace(value) || name.Length > 200 || value.Length > 20_000 || name.Any(char.IsControl) || value.Any(char.IsControl)) return;
        try
        {
            if (name.Equals("From", StringComparison.OrdinalIgnoreCase) || name.Equals("To", StringComparison.OrdinalIgnoreCase) ||
                name.Equals("Cc", StringComparison.OrdinalIgnoreCase) || name.Equals("Bcc", StringComparison.OrdinalIgnoreCase) ||
                name.Equals("Subject", StringComparison.OrdinalIgnoreCase) || name.Equals("Date", StringComparison.OrdinalIgnoreCase))
                return;
            message.Headers.Add(name, value);
        }
        catch (ParseException) { }
    }

    private static void AddMailboxes(InternetAddressList target, string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Length > 998) return;
        if (value.Any(char.IsControl)) return;
        try
        {
            if (!InternetAddressList.TryParse(value, out var addresses) || addresses is null) return;
            foreach (var mailbox in addresses.Mailboxes.Take(100))
            {
                if (mailbox.Address.Length <= 320 && mailbox.Address.Contains('@') && !mailbox.Address.Any(char.IsControl) &&
                    MailboxAddress.TryParse(mailbox.Address, out var validMailbox) && validMailbox is not null)
                    target.Add(new MailboxAddress(mailbox.Name, validMailbox.Address));
            }
        }
        catch (ParseException) { }
    }

    private static bool TryMailbox(string? value, out MailboxAddress? mailbox)
    {
        mailbox = null;
        if (string.IsNullOrWhiteSpace(value) || value.Length > 998) return false;
        try { mailbox = MailboxAddress.Parse(value); return true; }
        catch (ParseException) { return false; }
    }

    private static ContentType ParseContentType(string value)
    {
        try { return ContentType.Parse(string.IsNullOrWhiteSpace(value) ? "application/octet-stream" : value); }
        catch (ParseException) { return new ContentType("application", "octet-stream"); }
    }

    private static string? Clean(string? value, int max) => string.IsNullOrWhiteSpace(value) ? null : value.Trim()[..Math.Min(value.Trim().Length, max)];
}