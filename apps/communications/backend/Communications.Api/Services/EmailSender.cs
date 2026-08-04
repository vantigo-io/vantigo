using MailKit.Net.Smtp;
using MailKit.Security;
using MimeKit;
using Microsoft.Extensions.Hosting;

using Vantigo.Communications.Api.Database.Communications;

namespace Vantigo.Communications.Api.Services;

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
    Task SendAsync(EmailEnvelope envelope, CancellationToken cancellationToken);
}

public sealed class SmtpEmailSender(IConfiguration configuration, IHostEnvironment environment) : IEmailSender
{
    public async Task SendAsync(EmailEnvelope envelope, CancellationToken cancellationToken)
    {
        var message = new MimeMessage();
        // The Message-Id is stable across retries. SMTP submission remains
        // intentionally at-least-once: a process crash after relay acceptance
        // and before the durable completion update can submit the same message again.
        message.MessageId = $"<{envelope.MessageId:N}@vantigo.invalid>";
        message.From.Add(new MailboxAddress(envelope.FromDisplayName ?? string.Empty, envelope.FromAddress));
        foreach (var address in envelope.To) message.To.Add(MailboxAddress.Parse(address));
        foreach (var address in envelope.Cc) message.Cc.Add(MailboxAddress.Parse(address));
        foreach (var address in envelope.Bcc) message.Bcc.Add(MailboxAddress.Parse(address));
        message.Subject = envelope.Subject;
        var body = new BodyBuilder { TextBody = envelope.TextBody, HtmlBody = envelope.HtmlBody }.ToMessageBody();
        message.Body = body;

        var host = configuration["Smtp:Host"];
        if (string.IsNullOrWhiteSpace(host)) throw new InvalidOperationException("Smtp:Host is required.");
        var port = configuration.GetValue("Smtp:Port", 587);
        using var client = new SmtpClient();
        var leaseSeconds = Math.Max(1, configuration.GetValue("Outbox:LeaseSeconds", 60));
        var timeoutSeconds = configuration.GetValue("Smtp:TimeoutSeconds", Math.Max(1, leaseSeconds / 2));
        if (timeoutSeconds <= 0 || timeoutSeconds >= leaseSeconds)
            throw new InvalidOperationException("Smtp:TimeoutSeconds must be positive and less than Outbox:LeaseSeconds.");
        client.Timeout = checked(timeoutSeconds * 1000);
        using var timeoutCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeoutCts.CancelAfter(TimeSpan.FromSeconds(timeoutSeconds));
        var smtpCancellationToken = timeoutCts.Token;
        var useSsl = configuration.GetValue("Smtp:UseSsl", false);
        var allowPlaintext = configuration.GetValue("Smtp:AllowInsecurePlaintext", false);
        SecureSocketOptions socketOptions;
        if (allowPlaintext && environment.IsDevelopment())
            socketOptions = SecureSocketOptions.None;
        else if (useSsl)
            socketOptions = SecureSocketOptions.SslOnConnect;
        else if (port == 587)
            socketOptions = SecureSocketOptions.StartTls;
        else
            throw new InvalidOperationException("SMTP TLS is required. Use Smtp:UseSsl for implicit TLS or Smtp:AllowInsecurePlaintext only in Development.");
        await client.ConnectAsync(host, port, socketOptions, smtpCancellationToken);
        var username = configuration["Smtp:Username"];
        var password = configuration["Smtp:Password"];
        if (!string.IsNullOrWhiteSpace(username)) await client.AuthenticateAsync(username, password ?? string.Empty, smtpCancellationToken);
        await client.SendAsync(message, smtpCancellationToken);
        await client.DisconnectAsync(true, smtpCancellationToken);
    }
}

public static class EmailEnvelopeFactory
{
    public static EmailEnvelope Create(EmailMessage message, SharedMailbox mailbox)
    {
        return new EmailEnvelope(
            message.Id,
            mailbox.FromAddress,
            mailbox.DisplayName,
            message.Subject,
            message.TextBody,
            message.HtmlBody,
            message.Deliveries.Where(delivery => delivery.RecipientType == "to").Select(delivery => delivery.EmailAddress).ToArray(),
            message.Deliveries.Where(delivery => delivery.RecipientType == "cc").Select(delivery => delivery.EmailAddress).ToArray(),
            message.Deliveries.Where(delivery => delivery.RecipientType == "bcc").Select(delivery => delivery.EmailAddress).ToArray());
    }
}
