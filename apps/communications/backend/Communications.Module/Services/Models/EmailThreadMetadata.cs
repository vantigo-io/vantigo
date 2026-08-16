namespace Vantigo.Communications.Services;

public sealed record EmailThreadMetadata(string? RfcMessageId = null, string? InReplyTo = null, IReadOnlyList<string>? References = null,
    IReadOnlyList<string>? To = null, IReadOnlyList<string>? Cc = null);