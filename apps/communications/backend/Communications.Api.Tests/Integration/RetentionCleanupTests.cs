using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Api.Database.Communications;
using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Tests.Integration;

[Collection(CommunicationsApiCollection.Name)]
public sealed class RetentionCleanupTests(CommunicationsApiFactory factory)
{
    [Fact]
    public async Task Deletes_terminal_history_but_keeps_retrying_work()
    {
        using var scope = factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var mailbox = await db.SharedMailboxes.SingleAsync();
        var terminal = new EmailMessage { Id = Guid.NewGuid(), MailboxId = mailbox.Id, Subject = "old terminal", CreatedAt = DateTimeOffset.UtcNow.AddDays(-400) };
        var retrying = new EmailMessage { Id = Guid.NewGuid(), MailboxId = mailbox.Id, Subject = "old retry", CreatedAt = DateTimeOffset.UtcNow.AddDays(-400) };
        db.EmailMessages.AddRange(terminal, retrying);
        db.OutboxJobs.AddRange(
            new OutboxJob { Id = Guid.NewGuid(), MessageId = terminal.Id, Status = "completed", NextAttemptAt = DateTimeOffset.UtcNow, CreatedAt = terminal.CreatedAt },
            new OutboxJob { Id = Guid.NewGuid(), MessageId = retrying.Id, Status = "retry", NextAttemptAt = DateTimeOffset.UtcNow, CreatedAt = retrying.CreatedAt });
        await db.SaveChangesAsync();

        var deleted = await scope.ServiceProvider.GetRequiredService<RetentionCleanupService>()
            .CleanupBatchAsync(DateTimeOffset.UtcNow, CancellationToken.None);

        db.ChangeTracker.Clear();
        Assert.True(deleted >= 1);
        Assert.False(await db.EmailMessages.AnyAsync(item => item.Id == terminal.Id));
        Assert.True(await db.EmailMessages.AnyAsync(item => item.Id == retrying.Id));
    }
}
