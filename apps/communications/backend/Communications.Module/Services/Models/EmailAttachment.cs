namespace Vantigo.Communications.Services;

public sealed record EmailAttachment(string FileName, string ContentType, string StorageKey, long SizeBytes, string? ContentId, bool IsInline);