using MailKit.Net.Smtp;
using MailKit.Security;

using Microsoft.Extensions.Options;

using MimeKit;

using Vantigo.Configuration;

namespace Vantigo.Identity.Services;

/// <summary>
/// The application-owned mail seam. Invitation and recovery workflows depend on
/// this abstraction so a future central mail API can replace the sender without
/// changing account security code.
/// </summary>
public interface IApplicationEmailSender
{
    Task SendAsync(ApplicationEmail email, CancellationToken cancellationToken = default);
}

public sealed record ApplicationEmail(string To, string Subject, string TextBody);

public sealed class EmailOptions
{
    public string Provider { get; set; } = "Logging";
    public string From { get; set; } = "no-reply@localhost";
    public SmtpEmailOptions Smtp { get; set; } = new();
}

public sealed class SmtpEmailOptions
{
    public string? Host { get; set; }
    public int Port { get; set; } = 587;
    public string? UserName { get; set; }
    public string? Password { get; set; }
    public bool EnableSsl { get; set; } = true;
    public int TimeoutSeconds { get; set; } = 30;
}

/// <summary>
/// Temporary pre-production fallback sender. It deliberately logs the complete
/// sensitive message body, including invitation and password-reset bearer links,
/// so those workflows remain usable before a real mail provider is configured.
/// Replace this sender or configure SMTP before production use.
/// </summary>
public sealed class LoggingApplicationEmailSender(ILogger<LoggingApplicationEmailSender> logger)
    : IApplicationEmailSender
{
    public Task SendAsync(ApplicationEmail email, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        logger.LogInformation(
            "Application email queued to {Recipient}, subject {Subject}, body {Body}.",
            email.To,
            email.Subject,
            email.TextBody);
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