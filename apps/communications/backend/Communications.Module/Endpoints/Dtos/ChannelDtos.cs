namespace Vantigo.Communications.Endpoints;

internal sealed record CreateChannelRequest(
    string? Type,
    string? Address,
    string? DisplayName,
    string? Provider = "smtp",
    bool? IsDefault = null,
    SmtpChannelCredentialRequest? Smtp = null,
    MailgunChannelCredentialRequest? Mailgun = null);
internal sealed record UpdateChannelRequest(
    string? DisplayName,
    bool? IsActive,
    bool? IsDefault = null,
    string? Provider = null,
    SmtpChannelCredentialRequest? Smtp = null,
    MailgunChannelCredentialRequest? Mailgun = null);
internal sealed record SmtpChannelCredentialRequest(string? Host, int? Port, bool? UseSsl, string? Username, string? Password);
internal sealed record MailgunChannelCredentialRequest(string? Domain, string? Region, string? ApiKey, string? InboundSigningKey = null);
internal sealed record ChannelSettingsSummary(string? Host, int? Port, bool? UseSsl, string? Username, string? Domain, string? Region);
internal sealed record ChannelResponse(Guid Id, string Type, string Address, string? DisplayName, DateTimeOffset CreatedAt,
    bool IsActive, string Provider, bool IsDefault, bool HasCredentials, ChannelSettingsSummary? Settings);