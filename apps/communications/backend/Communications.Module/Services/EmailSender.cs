using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;

using MailKit.Net.Smtp;
using MailKit.Security;

using Microsoft.AspNetCore.DataProtection;
using Microsoft.Extensions.Hosting;

using MimeKit;

using Vantigo.Communications.Database.Communications;

namespace Vantigo.Communications.Services;

public sealed record EmailEnvelope(
    Guid MessageId,
    string FromAddress,
    string? FromDisplayName,
    string Subject,
    string? TextBody,
    string? HtmlBody,
    IReadOnlyList<string> To,
    IReadOnlyList<string> Cc,
    IReadOnlyList<string> Bcc);

public interface IEmailSender
{
    Task SendAsync(EmailEnvelope envelope, SharedMailbox mailbox, CancellationToken cancellationToken);
}

public interface IEmailDeliveryProvider
{
    string ProviderName { get; }
    Task SendAsync(EmailEnvelope envelope, MailboxProviderCredential? credential, CancellationToken cancellationToken);
}

public sealed class MailboxCredentialProtector(IDataProtectionProvider provider)
{
    private readonly IDataProtector protector = provider.CreateProtector("Communications.MailboxProvider.v1");

    public string Protect(string value) => protector.Protect(value);
    public string Unprotect(string value) => protector.Unprotect(value);
}

public sealed record SmtpProviderSettings(string Host, int Port, bool UseSsl, string? Username);
public sealed record MailgunProviderSettings(string Domain, string Region);

public sealed class SmtpDeliveryProvider(IConfiguration configuration, IHostEnvironment environment, MailboxCredentialProtector protector) : IEmailDeliveryProvider
{
    public string ProviderName => "smtp";

    public async Task SendAsync(EmailEnvelope envelope, MailboxProviderCredential? credential, CancellationToken cancellationToken)
    {
        await ExecuteAsync(credential, async (client, cancellationToken) =>
        {
            await client.SendAsync(CreateMessage(envelope), cancellationToken);
            await client.DisconnectAsync(true, cancellationToken);
        }, cancellationToken);
    }

    public async Task VerifyAsync(SharedMailbox mailbox, CancellationToken cancellationToken)
    {
        await ExecuteAsync(mailbox.Credential, (client, cancellationToken) => client.DisconnectAsync(true, cancellationToken), cancellationToken);
    }

    private async Task ExecuteAsync(MailboxProviderCredential? credential, Func<SmtpClient, CancellationToken, Task> operation, CancellationToken cancellationToken)
    {
        var settings = credential is null ? new SmtpProviderSettings(
            configuration["Smtp:Host"] ?? string.Empty,
            configuration.GetValue("Smtp:Port", 587),
            configuration.GetValue("Smtp:UseSsl", false),
            configuration["Smtp:Username"])
            : JsonSerializer.Deserialize<SmtpProviderSettings>(credential.SettingsJson, JsonOptions) ?? throw new InvalidOperationException("SMTP settings are invalid.");
        if (string.IsNullOrWhiteSpace(settings.Host)) throw new InvalidOperationException("Smtp:Host is required.");
        var leaseSeconds = Math.Max(1, configuration.GetValue("Outbox:LeaseSeconds", 60));
        var timeoutSeconds = configuration.GetValue("Smtp:TimeoutSeconds", Math.Max(1, leaseSeconds / 2));
        if (timeoutSeconds <= 0 || timeoutSeconds >= leaseSeconds)
            throw new InvalidOperationException("Smtp:TimeoutSeconds must be positive and less than Outbox:LeaseSeconds.");
        using var timeoutCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeoutCts.CancelAfter(TimeSpan.FromSeconds(timeoutSeconds));
        using var client = new SmtpClient { Timeout = checked(timeoutSeconds * 1000) };
        var allowPlaintext = configuration.GetValue("Smtp:AllowInsecurePlaintext", false);
        SecureSocketOptions socketOptions;
        if (allowPlaintext && environment.IsDevelopment()) socketOptions = SecureSocketOptions.None;
        else if (settings.UseSsl) socketOptions = SecureSocketOptions.SslOnConnect;
        else if (settings.Port == 587) socketOptions = SecureSocketOptions.StartTls;
        else
            throw new InvalidOperationException("SMTP TLS is required. Use Smtp:UseSsl for implicit TLS or Smtp:AllowInsecurePlaintext only in Development.");
        await client.ConnectAsync(settings.Host, settings.Port, socketOptions, timeoutCts.Token);
        var password = credential is null ? configuration["Smtp:Password"] ?? string.Empty : protector.Unprotect(credential.SecretCiphertext);
        if (!string.IsNullOrWhiteSpace(settings.Username)) await client.AuthenticateAsync(settings.Username, password, timeoutCts.Token);
        await operation(client, timeoutCts.Token);
    }

    private static MimeMessage CreateMessage(EmailEnvelope envelope)
    {
        var message = new MimeMessage { MessageId = $"<{envelope.MessageId:N}@vantigo.invalid>" };
        message.From.Add(new MailboxAddress(envelope.FromDisplayName ?? string.Empty, envelope.FromAddress));
        foreach (var address in envelope.To) message.To.Add(MailboxAddress.Parse(address));
        foreach (var address in envelope.Cc) message.Cc.Add(MailboxAddress.Parse(address));
        foreach (var address in envelope.Bcc) message.Bcc.Add(MailboxAddress.Parse(address));
        message.Subject = envelope.Subject;
        message.Body = new BodyBuilder { TextBody = envelope.TextBody, HtmlBody = envelope.HtmlBody }.ToMessageBody();
        return message;
    }

    internal static readonly JsonSerializerOptions JsonOptions = new() { PropertyNamingPolicy = JsonNamingPolicy.CamelCase };
}

public sealed class MailgunDeliveryProvider(IHttpClientFactory httpClientFactory, MailboxCredentialProtector protector) : IEmailDeliveryProvider
{
    public string ProviderName => "mailgun";

    public async Task SendAsync(EmailEnvelope envelope, MailboxProviderCredential? credential, CancellationToken cancellationToken)
    {
        if (credential is null) throw new InvalidOperationException("Mailgun credentials are required.");
        var settings = ReadSettings(credential);
        var apiKey = protector.Unprotect(credential.SecretCiphertext);
        using var request = new HttpRequestMessage(HttpMethod.Post, MessagesUri(settings));
        request.Headers.Authorization = BasicAuth(apiKey);
        using var form = new MultipartFormDataContent();
        Add(form, "from", FormatFrom(envelope));
        foreach (var address in envelope.To) Add(form, "to", address);
        foreach (var address in envelope.Cc) Add(form, "cc", address);
        foreach (var address in envelope.Bcc) Add(form, "bcc", address);
        Add(form, "subject", envelope.Subject);
        if (envelope.TextBody is not null) Add(form, "text", envelope.TextBody);
        if (envelope.HtmlBody is not null) Add(form, "html", envelope.HtmlBody);
        Add(form, "h:Message-Id", $"<{envelope.MessageId:N}@vantigo.invalid>");
        request.Content = form;
        await SendRequestAsync(request, cancellationToken);
    }

    public async Task VerifyAsync(SharedMailbox mailbox, CancellationToken cancellationToken)
    {
        if (mailbox.Credential is null) throw new InvalidOperationException("Mailgun credentials are required.");
        var settings = ReadSettings(mailbox.Credential);
        var apiKey = protector.Unprotect(mailbox.Credential.SecretCiphertext);
        using var request = new HttpRequestMessage(HttpMethod.Get, DomainsUri(settings));
        request.Headers.Authorization = BasicAuth(apiKey);
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

    private static MailgunProviderSettings ReadSettings(MailboxProviderCredential credential) =>
        JsonSerializer.Deserialize<MailgunProviderSettings>(credential.SettingsJson, SmtpDeliveryProvider.JsonOptions) ?? throw new InvalidOperationException("Mailgun settings are invalid.");

    private static string MessagesUri(MailgunProviderSettings settings) => $"https://api.{(settings.Region == "eu" ? "eu." : string.Empty)}mailgun.net/v3/{Uri.EscapeDataString(settings.Domain)}/messages";
    private static string DomainsUri(MailgunProviderSettings settings) => $"https://api.{(settings.Region == "eu" ? "eu." : string.Empty)}mailgun.net/v3/domains/{Uri.EscapeDataString(settings.Domain)}";
    private static AuthenticationHeaderValue BasicAuth(string apiKey) => new("Basic", Convert.ToBase64String(Encoding.UTF8.GetBytes($"api:{apiKey}")));
    private static string FormatFrom(EmailEnvelope envelope) => string.IsNullOrWhiteSpace(envelope.FromDisplayName) ? envelope.FromAddress : new MailboxAddress(envelope.FromDisplayName, envelope.FromAddress).ToString();
    private static void Add(MultipartFormDataContent form, string name, string value) => form.Add(new StringContent(value, Encoding.UTF8), name);
}

public sealed class ProviderDispatchingEmailSender(IEnumerable<IEmailDeliveryProvider> providers) : IEmailSender
{
    public Task SendAsync(EmailEnvelope envelope, SharedMailbox mailbox, CancellationToken cancellationToken)
    {
        var provider = providers.SingleOrDefault(item => string.Equals(item.ProviderName, mailbox.Provider, StringComparison.OrdinalIgnoreCase));
        if (provider is null) throw new InvalidOperationException($"Unknown mailbox provider '{mailbox.Provider}'.");
        return provider.SendAsync(envelope, mailbox.Credential, cancellationToken);
    }
}

public static class EmailEnvelopeFactory
{
    public static EmailEnvelope Create(EmailMessage message, SharedMailbox mailbox) => Create(message, mailbox, message.Deliveries);

    public static EmailEnvelope Create(EmailMessage message, SharedMailbox mailbox, IEnumerable<RecipientDelivery> deliveries) => new(
        message.Id,
        mailbox.FromAddress,
        mailbox.DisplayName,
        message.Subject,
        message.TextBody,
        message.HtmlBody,
        deliveries.Where(delivery => delivery.RecipientType == "to").Select(delivery => delivery.EmailAddress).ToArray(),
        deliveries.Where(delivery => delivery.RecipientType == "cc").Select(delivery => delivery.EmailAddress).ToArray(),
        deliveries.Where(delivery => delivery.RecipientType == "bcc").Select(delivery => delivery.EmailAddress).ToArray());
}