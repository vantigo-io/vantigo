namespace Vantigo.Communications.Services;

internal sealed record MailgunFormAttachment(string FileName, string ContentType, byte[] Bytes);