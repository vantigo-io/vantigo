using System.Net;
using System.Net.Http.Headers;
using System.Text.Json;

using Microsoft.AspNetCore.DataProtection;
using Microsoft.Extensions.Http;

using MimeKit;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Communications.Services;
using Vantigo.Storage.Abstractions;

namespace Vantigo.Communications.Module.Tests.Services;

public sealed class MailgunDeliveryProviderTests
{
    [Fact]
    public void Synthetic_mime_preserves_only_valid_bounded_cc_mailboxes()
    {
        var form = new MailgunInboundForm("1", "token", "signature", "sender@example.test", null, "inbound@example.test",
            "cc@example.test, <second@example.test>", "subject", "body", null, null, null, []);

        using var message = MimeMessage.Load(new MemoryStream(MailgunInboundMimeBuilder.CreateSynthetic(form, DateTimeOffset.UtcNow)));

        Assert.Equal(["cc@example.test", "second@example.test"], message.Cc.Mailboxes.Select(item => item.Address).ToArray());

        var malformed = form with { Cc = "cc@example.test\r\nBcc: injected@example.test" };
        using var safeMessage = MimeMessage.Load(new MemoryStream(MailgunInboundMimeBuilder.CreateSynthetic(malformed, DateTimeOffset.UtcNow)));
        Assert.Empty(safeMessage.Cc);
        Assert.DoesNotContain("injected@example.test", safeMessage.Headers.Select(item => item.Value));
    }

    [Fact]
    public void Persisted_thread_metadata_keeps_cc_reply_all_candidates()
    {
        var metadata = EmailEnvelopeFactory.ParseMetadata(JsonSerializer.Serialize(
            new EmailThreadMetadata(Cc: ["copy@example.test"]), SmtpDeliveryProvider.JsonOptions));

        Assert.Contains("copy@example.test", metadata.Cc!);
    }

    [Theory]
    [InlineData("us", "https://api.mailgun.net/v3/example.test/messages")]
    [InlineData("eu", "https://api.eu.mailgun.net/v3/example.test/messages")]
    public async Task Sends_to_the_regional_mailgun_endpoint_with_expected_auth_and_fields(string region, string expectedUri)
    {
        var handler = new RecordingHandler(new HttpResponseMessage(HttpStatusCode.OK));
        var protector = new MailboxCredentialProtector(new EphemeralDataProtectionProvider());
        var provider = CreateProvider(handler, protector);
        var envelope = new EmailEnvelope(
            Guid.Parse("11111111-1111-1111-1111-111111111111"),
            "sender@example.test",
            "Sender",
            "Subject",
            "plain text",
            "<p>html</p>",
            ["to@example.test"],
            ["cc@example.test"],
            ["bcc@example.test"]);
        var credential = new ChannelCredential
        {
            Id = Guid.NewGuid(),
            ChannelId = Guid.NewGuid(),
            SettingsJson = JsonSerializer.Serialize(new MailgunProviderSettings("example.test", region), SmtpDeliveryProvider.JsonOptions),
            SecretCiphertext = protector.Protect("mailgun-test-key"),
            CreatedAt = DateTimeOffset.UtcNow,
        };

        await provider.SendAsync(envelope, credential, CancellationToken.None);

        Assert.Equal(expectedUri, handler.Request!.RequestUri!.ToString());
        Assert.Equal(HttpMethod.Post, handler.Request.Method);
        Assert.Equal("Basic", handler.Request.Headers.Authorization!.Scheme);
        Assert.Equal(Convert.ToBase64String("api:mailgun-test-key"u8.ToArray()), handler.Request.Headers.Authorization.Parameter);
        var form = handler.FormBody!;
        Assert.Contains("name=from", form);
        Assert.Contains("sender@example.test", form);
        Assert.Contains("name=to", form);
        Assert.Contains("to@example.test", form);
        Assert.Contains("name=cc", form);
        Assert.Contains("cc@example.test", form);
        Assert.Contains("name=bcc", form);
        Assert.Contains("bcc@example.test", form);
        Assert.Contains("name=subject", form);
        Assert.Contains("Subject", form);
        Assert.Contains("name=text", form);
        Assert.Contains("plain text", form);
        Assert.Contains("name=html", form);
        Assert.Contains("<p>html</p>", form);
    }

    [Fact]
    public async Task Throws_with_http_status_when_mailgun_returns_an_error()
    {
        var handler = new RecordingHandler(new HttpResponseMessage(HttpStatusCode.InternalServerError)
        {
            ReasonPhrase = "Server Error",
            Content = new StringContent("provider failure"),
        });
        var protector = new MailboxCredentialProtector(new EphemeralDataProtectionProvider());
        var provider = CreateProvider(handler, protector);
        var credential = CreateCredential("us", "mailgun-test-key", protector);

        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => provider.SendAsync(
            new EmailEnvelope(Guid.NewGuid(), "sender@example.test", null, "subject", "body", null, ["to@example.test"], [], []),
            credential,
            CancellationToken.None));

        Assert.Contains("500", exception.Message);
        Assert.Contains("provider failure", exception.Message);
    }

    private static MailgunDeliveryProvider CreateProvider(RecordingHandler handler, MailboxCredentialProtector protector) =>
        new(new StubHttpClientFactory(handler), protector, new EmptyObjectStore());

    private static ChannelCredential CreateCredential(string region, string apiKey, MailboxCredentialProtector protector)
    {
        return new ChannelCredential
        {
            Id = Guid.NewGuid(),
            ChannelId = Guid.NewGuid(),
            SettingsJson = JsonSerializer.Serialize(new MailgunProviderSettings("example.test", region), SmtpDeliveryProvider.JsonOptions),
            SecretCiphertext = protector.Protect(apiKey),
            CreatedAt = DateTimeOffset.UtcNow,
        };
    }

    private sealed class StubHttpClientFactory(HttpMessageHandler handler) : IHttpClientFactory
    {
        public HttpClient CreateClient(string name) => new(handler, disposeHandler: false);
    }

    private sealed class RecordingHandler(HttpResponseMessage response) : HttpMessageHandler
    {
        public HttpRequestMessage? Request { get; private set; }
        public string? FormBody { get; private set; }

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            Request = request;
            FormBody = request.Content is null ? null : await request.Content.ReadAsStringAsync(cancellationToken);
            return response;
        }
    }

    private sealed class EmptyObjectStore : IObjectStore<CommunicationsStorageScope>
    {
        public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default) => Task.CompletedTask;
        public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) => Task.FromResult<Stream?>(null);
        public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) => Task.FromResult(false);
        public Task DeleteAsync(string key, CancellationToken cancellationToken = default) => Task.CompletedTask;
    }
}