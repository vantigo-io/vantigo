namespace Vantigo.Contracts.Email;

/// <summary>
/// The application-owned mail seam. Invitation and recovery workflows depend on
/// this abstraction so a future central mail API can replace the sender without
/// changing account security code.
/// </summary>
public interface IApplicationEmailSender
{
    Task SendAsync(ApplicationEmail email, CancellationToken cancellationToken = default);
}