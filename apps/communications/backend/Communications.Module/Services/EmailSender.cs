using System.Net;
using System.Net.Http.Headers;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;

using MailKit.Net.Smtp;
using MailKit.Security;

using Microsoft.AspNetCore.DataProtection;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using MimeKit;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Configuration;
using Vantigo.Storage.Abstractions;

namespace Vantigo.Communications.Services;

internal static class EmailMessageId
{
    internal static string For(Guid messageId) => $"<{messageId:N}@vantigo.invalid>";
}

internal interface IEmailSender
{
    Task SendAsync(EmailEnvelope envelope, Channel channel, CancellationToken cancellationToken);
}

internal interface IOutboundChannelAdapter
{
    string ChannelType { get; }
    Task SendAsync(ConversationMessage message, Conversation conversation, Channel channel, CancellationToken cancellationToken);
}

internal sealed class EmailOutboundChannelAdapter(IEmailSender sender) : IOutboundChannelAdapter
{
    public string ChannelType => "email";
    public Task SendAsync(ConversationMessage message, Conversation conversation, Channel channel, CancellationToken cancellationToken)
    {
        if (!string.Equals(channel.Type, "email", StringComparison.OrdinalIgnoreCase))
            throw new InvalidOperationException($"No email adapter exists for channel type '{channel.Type}'.");
        return sender.SendAsync(EmailEnvelopeFactory.Create(message, conversation, channel), channel, cancellationToken);
    }
}

internal interface IInboundChannelAdapter { }

internal interface IThreadResolver
{
    Task<Guid?> ResolveAsync(Guid channelId, NormalizedInboundMessage message, CancellationToken cancellationToken);
}

internal sealed class EmailThreadResolver(CommunicationsDbContext db) : IThreadResolver
{
    public async Task<Guid?> ResolveAsync(Guid channelId, NormalizedInboundMessage inbound, CancellationToken cancellationToken)
    {
        var headers = new[] { inbound.InReplyTo }.Concat(inbound.References ?? []).Where(item => !string.IsNullOrWhiteSpace(item)).Select(NormalizeMessageId).ToArray();
        if (headers.Length > 0)
        {
            var match = await db.ConversationMessages.AsNoTracking()
                .Where(item => item.Conversation!.ChannelId == channelId && item.RfcMessageId != null)
                .Where(item => headers.Contains(item.RfcMessageId!.Trim().Trim('<', '>').ToLower()))
                .Select(item => (Guid?)item.ConversationId).FirstOrDefaultAsync(cancellationToken);
            if (match.HasValue) return match;
        }

        var subject = ThreadSubject.Normalize(inbound.Subject);
        if (string.IsNullOrEmpty(subject)) return null;
        var cutoff = inbound.OccurredAt.AddDays(-30);
        var mailbox = await db.Channels.AsNoTracking().Where(item => item.Id == channelId).Select(item => item.Address).SingleAsync(cancellationToken);
        var addresses = new[] { inbound.FromAddress }.Concat(inbound.To ?? []).Concat(inbound.Cc ?? [])
            .Where(address => !string.Equals(EmailSuppression.Normalize(address), EmailSuppression.Normalize(mailbox), StringComparison.Ordinal))
            .Select(EmailSuppression.Normalize).Distinct(StringComparer.Ordinal).ToArray();
        var candidates = await db.Conversations.AsNoTracking()
            .Where(item => item.ChannelId == channelId && item.LastActivityAt >= cutoff)
            .Include(item => item.Participants).ThenInclude(item => item.Participant)
            .OrderByDescending(item => item.LastActivityAt)
            .ToListAsync(cancellationToken);
        return candidates.FirstOrDefault(item => ThreadSubject.Normalize(item.Subject) == subject &&
            item.Participants.Any(link => link.Participant is not null &&
                !string.Equals(EmailSuppression.Normalize(link.Participant.Address), EmailSuppression.Normalize(mailbox), StringComparison.Ordinal) &&
                addresses.Contains(EmailSuppression.Normalize(link.Participant.Address))))?.Id;
    }

    internal static string NormalizeMessageId(string? value) => (value ?? string.Empty).Trim().Trim('<', '>').ToLowerInvariant();
}

public static class ThreadSubject
{
    public static string Normalize(string? subject)
    {
        var value = (subject ?? string.Empty).Trim();
        while (true)
        {
            var next = value.StartsWith("re:", StringComparison.OrdinalIgnoreCase) ? value[3..].TrimStart()
                : value.StartsWith("fwd:", StringComparison.OrdinalIgnoreCase) ? value[4..].TrimStart()
                : value.StartsWith("fw:", StringComparison.OrdinalIgnoreCase) ? value[3..].TrimStart() : value;
            if (next == value) break;
            value = next;
        }
        return string.Concat(value.Where(character => !char.IsWhiteSpace(character))).ToUpperInvariant();
    }
}

internal interface IOutboundChannelAdapterRegistry
{
    IOutboundChannelAdapter Get(string channelType);
}

internal sealed class OutboundChannelAdapterRegistry(IEnumerable<IOutboundChannelAdapter> adapters) : IOutboundChannelAdapterRegistry
{
    private readonly IReadOnlyDictionary<string, IOutboundChannelAdapter> adapterMap = adapters.ToDictionary(adapter => adapter.ChannelType, StringComparer.OrdinalIgnoreCase);

    public IOutboundChannelAdapter Get(string channelType) => adapterMap.TryGetValue(channelType, out var adapter)
        ? adapter
        : throw new InvalidOperationException($"No outbound adapter is registered for channel type '{channelType}'.");
}

public sealed class MailboxCredentialProtector(IDataProtectionProvider provider)
{
    private readonly IDataProtector protector = provider.CreateProtector("Communications.MailboxProvider.v1");
    public string Protect(string value) => protector.Protect(value);
    public string Unprotect(string value) => protector.Unprotect(value);
}

internal sealed record MailgunCredentialSecrets(string ApiKey, string? InboundSigningKey = null);

internal static class MailgunCredentialSecretReader
{
    internal static MailgunCredentialSecrets Read(MailboxCredentialProtector protector, ChannelCredential credential)
    {
        var value = protector.Unprotect(credential.SecretCiphertext);
        try
        {
            var secrets = JsonSerializer.Deserialize<MailgunCredentialSecrets>(value, SmtpDeliveryProvider.JsonOptions);
            if (!string.IsNullOrWhiteSpace(secrets?.ApiKey)) return secrets;
        }
        catch (JsonException) { }
        // Credentials written before inbound Mailgun support contain the API key
        // directly. Keep those credentials valid for outbound delivery.
        return new MailgunCredentialSecrets(value);
    }
}

internal sealed class SmtpDeliveryProvider(
    IOptions<SmtpOptions> options,
    IOptions<OutboxOptions> outboxOptions,
    IHostEnvironment environment,
    MailboxCredentialProtector protector,
    IObjectStore<CommunicationsStorageScope> objectStore,
    ISmtpDestinationGuard destinationGuard) : IEmailDeliveryProvider
{
    private readonly SmtpOptions smtp = options.Value;
    private readonly OutboxOptions outbox = outboxOptions.Value;
    public string ProviderName => "smtp";

    public async Task SendAsync(EmailEnvelope envelope, ChannelCredential? credential, CancellationToken cancellationToken)
    {
        await ExecuteAsync(credential, async (client, token) =>
        {
            using var message = await CreateMessageAsync(envelope, objectStore, token);
            await client.SendAsync(message, token);
            await client.DisconnectAsync(true, token);
        }, cancellationToken);
    }

    public async Task VerifyAsync(Channel channel, CancellationToken cancellationToken) => await ExecuteAsync(channel.Credential, (client, token) => client.DisconnectAsync(true, token), cancellationToken);

    private async Task ExecuteAsync(ChannelCredential? credential, Func<SmtpClient, CancellationToken, Task> operation, CancellationToken cancellationToken)
    {
        var settings = credential is null ? new SmtpProviderSettings(smtp.Host ?? string.Empty, smtp.Port, smtp.UseSsl, smtp.Username)
            : JsonSerializer.Deserialize<SmtpProviderSettings>(credential.SettingsJson, JsonOptions) ?? throw new InvalidOperationException("SMTP settings are invalid.");
        if (string.IsNullOrWhiteSpace(settings.Host)) throw new InvalidOperationException("Smtp:Host is required.");
        var leaseSeconds = Math.Max(1, outbox.LeaseSeconds);
        var timeoutSeconds = smtp.TimeoutSeconds > 0 ? smtp.TimeoutSeconds : Math.Max(1, leaseSeconds / 2);
        if (timeoutSeconds <= 0 || timeoutSeconds >= leaseSeconds) throw new InvalidOperationException("Smtp:TimeoutSeconds must be positive and less than Outbox:LeaseSeconds.");
        using var timeoutCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeoutCts.CancelAfter(TimeSpan.FromSeconds(timeoutSeconds));
        using var client = new SmtpClient { Timeout = checked(timeoutSeconds * 1000) };
        SecureSocketOptions socketOptions = environment.IsDevelopment() && smtp.AllowInsecurePlaintext ? SecureSocketOptions.None
            : settings.UseSsl ? SecureSocketOptions.SslOnConnect : settings.Port == 587 ? SecureSocketOptions.StartTls
            : throw new InvalidOperationException("SMTP TLS is required. Use Smtp:UseSsl for implicit TLS or Smtp:AllowInsecurePlaintext only in Development.");

        // Vet the destination as the very last step before connecting, and connect
        // the socket to the vetted IPAddress directly - never by hostname - so a
        // DNS answer that changes between the check and the connect (a rebinding
        // attack) cannot slip a private address past this guard. The hostname is
        // still handed to MailKit here so it drives TLS SNI and certificate
        // validation as normal.
        IPAddress destination = await destinationGuard.VetAsync(settings.Host, timeoutCts.Token);
        using Socket socket = new(destination.AddressFamily, SocketType.Stream, ProtocolType.Tcp);
        await socket.ConnectAsync(new IPEndPoint(destination, settings.Port), timeoutCts.Token);
        await client.ConnectAsync(socket, settings.Host, settings.Port, socketOptions, timeoutCts.Token);
        var password = credential is null ? smtp.Password ?? string.Empty : protector.Unprotect(credential.SecretCiphertext);
        if (!string.IsNullOrWhiteSpace(settings.Username)) await client.AuthenticateAsync(settings.Username, password, timeoutCts.Token);
        await operation(client, timeoutCts.Token);
    }

    internal static async Task<MimeMessage> CreateMessageAsync(EmailEnvelope envelope, IObjectStore<CommunicationsStorageScope>? objectStore, CancellationToken cancellationToken)
    {
        var message = new MimeMessage { MessageId = EmailMessageId.For(envelope.MessageId) };
        message.From.Add(new MailboxAddress(envelope.FromDisplayName ?? string.Empty, envelope.FromAddress));
        foreach (var address in envelope.To) message.To.Add(MailboxAddress.Parse(address));
        foreach (var address in envelope.Cc) message.Cc.Add(MailboxAddress.Parse(address));
        foreach (var address in envelope.Bcc) message.Bcc.Add(MailboxAddress.Parse(address));
        message.Subject = envelope.Subject;
        if (!string.IsNullOrWhiteSpace(envelope.InReplyTo)) message.InReplyTo = envelope.InReplyTo;
        if (envelope.References is { Count: > 0 }) message.References.AddRange(envelope.References);
        var builder = new BodyBuilder { TextBody = envelope.TextBody, HtmlBody = envelope.HtmlBody };
        foreach (var attachment in envelope.Attachments ?? [])
        {
            if (objectStore is null) throw new InvalidOperationException("Attachment storage is required.");
            var content = await objectStore.GetAsync(attachment.StorageKey, cancellationToken) ?? throw new InvalidOperationException("Attachment object is unavailable.");
            var part = new MimePart(attachment.ContentType) { FileName = attachment.FileName, Content = new MimeContent(content), ContentTransferEncoding = ContentEncoding.Base64 };
            if (attachment.IsInline && !string.IsNullOrWhiteSpace(attachment.ContentId)) part.ContentId = attachment.ContentId;
            builder.Attachments.Add(part);
        }
        message.Body = builder.ToMessageBody();
        return message;
    }

    internal static readonly JsonSerializerOptions JsonOptions = new() { PropertyNamingPolicy = JsonNamingPolicy.CamelCase };
}

internal interface IEmailDeliveryProvider
{
    string ProviderName { get; }
    Task SendAsync(EmailEnvelope envelope, ChannelCredential? credential, CancellationToken cancellationToken);
    Task VerifyAsync(Channel channel, CancellationToken cancellationToken);
}

internal sealed class MailgunDeliveryProvider(IHttpClientFactory httpClientFactory, MailboxCredentialProtector protector, IObjectStore<CommunicationsStorageScope> objectStore) : IEmailDeliveryProvider
{
    public string ProviderName => "mailgun";
    public async Task SendAsync(EmailEnvelope envelope, ChannelCredential? credential, CancellationToken cancellationToken)
    {
        if (credential is null) throw new InvalidOperationException("Mailgun credentials are required.");
        var settings = ReadSettings(credential);
        using var request = new HttpRequestMessage(HttpMethod.Post, MessagesUri(settings));
        request.Headers.Authorization = BasicAuth(MailgunCredentialSecretReader.Read(protector, credential).ApiKey);
        using var form = new MultipartFormDataContent();
        Add(form, "from", FormatFrom(envelope));
        foreach (var address in envelope.To) Add(form, "to", address);
        foreach (var address in envelope.Cc) Add(form, "cc", address);
        foreach (var address in envelope.Bcc) Add(form, "bcc", address);
        Add(form, "subject", envelope.Subject);
        if (envelope.TextBody is not null) Add(form, "text", envelope.TextBody);
        if (envelope.HtmlBody is not null) Add(form, "html", envelope.HtmlBody);
        Add(form, "h:Message-Id", EmailMessageId.For(envelope.MessageId));
        if (!string.IsNullOrWhiteSpace(envelope.InReplyTo)) Add(form, "h:In-Reply-To", envelope.InReplyTo);
        if (envelope.References is { Count: > 0 }) Add(form, "h:References", string.Join(" ", envelope.References));
        foreach (var attachment in envelope.Attachments ?? [])
        {
            if (objectStore is null) throw new InvalidOperationException("Attachment storage is required.");
            var content = await objectStore.GetAsync(attachment.StorageKey, cancellationToken) ?? throw new InvalidOperationException("Attachment object is unavailable.");
            form.Add(new StreamContent(content), attachment.IsInline ? "inline" : "attachment", attachment.FileName);
        }
        request.Content = form;
        await SendRequestAsync(request, cancellationToken);
    }
    public async Task VerifyAsync(Channel channel, CancellationToken cancellationToken)
    {
        if (channel.Credential is null) throw new InvalidOperationException("Mailgun credentials are required.");
        var settings = ReadSettings(channel.Credential);
        using var request = new HttpRequestMessage(HttpMethod.Get, DomainsUri(settings));
        request.Headers.Authorization = BasicAuth(MailgunCredentialSecretReader.Read(protector, channel.Credential).ApiKey);
        await SendRequestAsync(request, cancellationToken);
    }
    private async Task SendRequestAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        using var response = await httpClientFactory.CreateClient("mailgun").SendAsync(request, cancellationToken);
        if (response.IsSuccessStatusCode) return;
        var body = await response.Content.ReadAsStringAsync(cancellationToken);
        if (body.Length > 2000) body = body[..2000];
        throw new InvalidOperationException($"Mailgun returned {(int)response.StatusCode} {response.ReasonPhrase}: {body}");
    }
    private static MailgunProviderSettings ReadSettings(ChannelCredential credential) => JsonSerializer.Deserialize<MailgunProviderSettings>(credential.SettingsJson, SmtpDeliveryProvider.JsonOptions) ?? throw new InvalidOperationException("Mailgun settings are invalid.");
    private static string MessagesUri(MailgunProviderSettings settings) => $"https://api.{(settings.Region == "eu" ? "eu." : string.Empty)}mailgun.net/v3/{Uri.EscapeDataString(settings.Domain)}/messages";
    private static string DomainsUri(MailgunProviderSettings settings) => $"https://api.{(settings.Region == "eu" ? "eu." : string.Empty)}mailgun.net/v3/domains/{Uri.EscapeDataString(settings.Domain)}";
    private static AuthenticationHeaderValue BasicAuth(string apiKey) => new("Basic", Convert.ToBase64String(Encoding.UTF8.GetBytes($"api:{apiKey}")));
    private static string FormatFrom(EmailEnvelope envelope) => string.IsNullOrWhiteSpace(envelope.FromDisplayName) ? envelope.FromAddress : new MailboxAddress(envelope.FromDisplayName, envelope.FromAddress).ToString();
    private static void Add(MultipartFormDataContent form, string name, string value) => form.Add(new StringContent(value, Encoding.UTF8), name);
}

internal sealed class ProviderDispatchingEmailSender(IEnumerable<IEmailDeliveryProvider> providers) : IEmailSender
{
    public Task SendAsync(EmailEnvelope envelope, Channel channel, CancellationToken cancellationToken)
    {
        var provider = providers.SingleOrDefault(item => string.Equals(item.ProviderName, channel.Provider, StringComparison.OrdinalIgnoreCase));
        if (provider is null) throw new InvalidOperationException($"Unknown channel provider '{channel.Provider}'.");
        return provider.SendAsync(envelope, channel.Credential, cancellationToken);
    }
}

internal static class EmailEnvelopeFactory
{
    public static EmailEnvelope Create(ConversationMessage message, Conversation conversation, Channel channel) => Create(message, conversation, channel, message.Deliveries);
    public static EmailEnvelope Create(ConversationMessage message, Conversation conversation, Channel channel, IEnumerable<MessageDelivery> deliveries)
    {
        var metadata = ParseMetadata(message.ChannelMetadataJson);
        return new EmailEnvelope(message.Id, channel.Address, channel.DisplayName, message.Subject ?? conversation.Subject ?? string.Empty,
            message.TextBody, message.HtmlBody,
            deliveries.Where(item => item.RecipientType == "to").Select(item => item.RecipientAddress).ToArray(),
            deliveries.Where(item => item.RecipientType == "cc").Select(item => item.RecipientAddress).ToArray(),
            deliveries.Where(item => item.RecipientType == "bcc").Select(item => item.RecipientAddress).ToArray(),
            metadata.InReplyTo, metadata.References ?? [], message.Attachments.Where(item => item.ScanStatus == "clean").Select(item => new EmailAttachment(item.FileName, item.ContentType, item.StorageKey, item.SizeBytes, item.ContentId, item.IsInline)).ToArray());
    }
    internal static EmailThreadMetadata ParseMetadata(string? json) => string.IsNullOrWhiteSpace(json) ? new() : JsonSerializer.Deserialize<EmailThreadMetadata>(json, SmtpDeliveryProvider.JsonOptions) ?? new();
}