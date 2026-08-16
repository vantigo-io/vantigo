namespace Vantigo.Communications.Services;

public sealed class MailgunInboundOptions
{
    public int TimestampToleranceSeconds { get; set; } = 300;
    public int MaxFutureSkewSeconds { get; set; } = 300;
    public int MaxPastSkewSeconds { get; set; } = 300;
    public int MaxRequestBytes { get; set; } = 25 * 1024 * 1024;
    public int MaxMimeBytes { get; set; } = 25 * 1024 * 1024;
    public int MaxAttachmentBytes { get; set; } = 10 * 1024 * 1024;
    public int MaxAggregateAttachmentBytes { get; set; } = 10 * 1024 * 1024;
    public int MaxAttachments { get; set; } = 20;
    public int MaxBodyBytes { get; set; } = 8 * 1024 * 1024;
    public int MaxFormKeys { get; set; } = 100;
    public int MaxHeaderBytes { get; set; } = 512 * 1024;
    public int MaxAttachedMessageParts { get; set; } = 0;
    public int LeaseSeconds { get; set; } = 120;
    public int ClaimAttempts { get; set; } = 10;
    public int MaxAttempts { get; set; } = 8;
    public int PollSeconds { get; set; } = 5;
}