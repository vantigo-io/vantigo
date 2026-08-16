namespace Vantigo.Communications.Services;

public sealed record NormalizedInboundAttachment(string? ContentId, string FileName, string ContentType, byte[] Bytes);