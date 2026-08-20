using MailKit.Net.Smtp;
using MailKit.Security;

using Microsoft.Extensions.Options;

using MimeKit;

using Vantigo.Configuration;

namespace Vantigo.Identity.Services;

/// <summary>
/// Development-only fallback sender, rejected outside Development by
/// <c>EmailOptionsValidator</c>. It never logs the message body, subject, or any
/// token/link the body may contain (invitation and password-reset workflows embed
/// bearer links there) - only the recipient and a generated correlation id, so it
/// remains safe to leave enabled locally without leaking credentials into logs.
/// </summary>
public sealed class LoggingApplicationEmailSender(ILogger<LoggingApplicationEmailSender> logger)
    : IApplicationEmailSender
{
    public Task SendAsync(ApplicationEmail email, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        logger.LogInformation(
            "Application email {MessageId} queued to {Recipient}.",
            Guid.NewGuid(),
            email.To);
        return Task.CompletedTask;
    }
}

/// <summary>
/// Optional direct SMTP sender. It is deliberately configured through ordinary
/// Email:Smtp settings and can be replaced by a central API adapter later.
/// </summary>
public sealed class SmtpApplicationEmailSender(
    IOptions<EmailOptions> options,
    ILogger<SmtpApplicationEmailSender> logger) : IApplicationEmailSender
{
    public async Task SendAsync(ApplicationEmail email, CancellationToken cancellationToken = default)
    {
        var smtp = options.Value.Smtp;
        if (string.IsNullOrWhiteSpace(smtp.Host))
        {
            throw new InvalidOperationException("Email:Smtp:Host is required when Email:Provider is Smtp.");
        }

        var message = new MimeMessage();
        message.From.Add(MailboxAddress.Parse(options.Value.From));
        message.To.Add(MailboxAddress.Parse(email.To));
        message.Subject = email.Subject;
        message.Body = new TextPart("plain") { Text = email.TextBody };

        using var client = new SmtpClient
        {
            Timeout = Math.Clamp(smtp.TimeoutSeconds, 1, 300) * 1000,
        };

        try
        {
            var socketOptions = smtp.EnableSsl ? SecureSocketOptions.StartTls : SecureSocketOptions.Auto;
            await client.ConnectAsync(smtp.Host, smtp.Port, socketOptions, cancellationToken);
            if (!string.IsNullOrWhiteSpace(smtp.UserName))
            {
                await client.AuthenticateAsync(smtp.UserName, smtp.Password ?? string.Empty, cancellationToken);
            }

            await client.SendAsync(message, cancellationToken);
            await client.DisconnectAsync(true, cancellationToken);
        }
        catch (OperationCanceledException)
        {
            throw;
        }
        catch (Exception exception)
        {
            logger.LogError(exception, "Application email delivery failed for {Recipient}.", email.To);
            throw;
        }
    }
}