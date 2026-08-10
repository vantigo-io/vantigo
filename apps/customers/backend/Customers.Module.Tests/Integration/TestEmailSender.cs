using System.Collections.Concurrent;

using Vantigo.Identity.Services;

namespace Vantigo.Customers.Module.Tests.Integration;

internal sealed class CapturingEmailSender : IApplicationEmailSender
{
    private readonly ConcurrentQueue<ApplicationEmail> _messages = new();

    public bool Fail { get; set; }

    public IReadOnlyCollection<ApplicationEmail> Messages => _messages.ToArray();

    public Task SendAsync(ApplicationEmail email, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        if (Fail)
        {
            throw new InvalidOperationException("Test email delivery failure.");
        }

        _messages.Enqueue(email);
        return Task.CompletedTask;
    }

    public void Clear()
    {
        while (_messages.TryDequeue(out _))
        {
        }
    }
}