using System.Net;
using System.Net.Http.Json;
using System.Text.Json;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;

namespace Vantigo.Communications.Module.Tests.Integration;

/// <summary>
/// Covers the host-wide exception pipeline through a real module endpoint: the
/// communications factory boots the same host as production, so an unhandled
/// failure inside an endpoint exercises the global exception handler end to end.
/// </summary>
[Collection(CommunicationsModuleCollection.Name)]
public sealed class GlobalExceptionHandlingTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task Unhandled_endpoint_failures_become_sanitized_problem_details()
    {
        await factory.ResetChannelStateAsync();
        Guid conversationId = await CreateConversationAsync();

        using HttpClient client = await factory.CreateAuthenticatedClientAsync();
        factory.CustomerDirectory.FindCustomerFailure =
            new InvalidOperationException("customer directory credentials rejected");
        try
        {
            using HttpResponseMessage response = await client.PatchAsJsonAsync(
                $"/api/v1/communications/conversations/{conversationId}",
                new { customerId = 7 });

            Assert.Equal(HttpStatusCode.InternalServerError, response.StatusCode);
            Assert.Equal("application/problem+json", response.Content.Headers.ContentType?.MediaType);

            string body = await response.Content.ReadAsStringAsync();
            Assert.DoesNotContain("credentials rejected", body, StringComparison.Ordinal);
            Assert.DoesNotContain("InvalidOperationException", body, StringComparison.Ordinal);

            JsonElement problem = JsonDocument.Parse(body).RootElement;
            Assert.Equal(500, problem.GetProperty("status").GetInt32());
            Assert.False(string.IsNullOrWhiteSpace(problem.GetProperty("title").GetString()));
            Assert.False(string.IsNullOrWhiteSpace(problem.GetProperty("traceId").GetString()));
        }
        finally
        {
            factory.CustomerDirectory.FindCustomerFailure = null;
        }
    }

    private async Task<Guid> CreateConversationAsync()
    {
        await using AsyncServiceScope scope = factory.Services.CreateAsyncScope();
        CommunicationsDbContext db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        Channel channel = await db.Channels.SingleAsync();
        DateTimeOffset now = DateTimeOffset.UtcNow;
        Conversation conversation = new()
        {
            Id = Guid.NewGuid(),
            ChannelId = channel.Id,
            Subject = "Unhandled failure",
            Status = "open",
            LastActivityAt = now,
            CreatedAt = now,
        };
        db.Conversations.Add(conversation);
        await db.SaveChangesAsync();
        return conversation.Id;
    }
}