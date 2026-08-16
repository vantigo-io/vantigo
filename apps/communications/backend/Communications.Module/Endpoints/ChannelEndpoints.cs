using System.Text.Json;

using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Authorization;
using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;
using Vantigo.Contracts.AspNetCore.Authorization;

using static Vantigo.Communications.Endpoints.CommunicationEndpointHelpers;

namespace Vantigo.Communications.Endpoints;

internal static class ChannelEndpoints
{
    internal static void MapChannelEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapGet("/channels", ListChannels).RequirePermission(CommunicationsPermissions.ChannelsManage);
        api.MapGet("/channels/{id:guid}", GetChannel).RequirePermission(CommunicationsPermissions.ChannelsManage);
        api.MapPost("/channels", CreateChannel).RequirePermission(CommunicationsPermissions.ChannelsManage);
        api.MapPut("/channels/{id:guid}", UpdateChannel).RequirePermission(CommunicationsPermissions.ChannelsManage);
        api.MapPost("/channels/{id:guid}/verify", VerifyChannel).RequirePermission(CommunicationsPermissions.ChannelsManage);
    }

    private static async Task<IResult> ListChannels(CommunicationsDbContext db, CancellationToken ct) => TypedResults.Ok((await db.Channels.AsNoTracking().Include(item => item.Credential).OrderBy(item => item.CreatedAt).ToListAsync(ct)).Select(ToChannelResponse).ToArray());
    private static async Task<IResult> GetChannel(Guid id, CommunicationsDbContext db, CancellationToken ct) { var channel = await db.Channels.AsNoTracking().Include(item => item.Credential).SingleOrDefaultAsync(item => item.Id == id, ct); return channel is null ? TypedResults.NotFound() : TypedResults.Ok(ToChannelResponse(channel)); }
    private static async Task<IResult> CreateChannel(CreateChannelRequest? request, HttpContext http, CommunicationsDbContext db, MailboxCredentialProtector protector, CancellationToken ct)
    {
        var errors = CommunicationValidation.ValidateChannel(request); if (errors.Count > 0) return ValidationError(errors); var now = DateTimeOffset.UtcNow; var channel = new Channel { Id = Guid.NewGuid(), Type = request!.Type!.Trim().ToLowerInvariant(), Address = request.Address!.Trim(), DisplayName = request.DisplayName?.Trim(), Provider = CommunicationValidation.ProviderName(request.Provider), IsDefault = request.IsDefault == true || !await db.Channels.AnyAsync(ct), CreatedAt = now }; AddCredential(channel, request.Smtp, request.Mailgun, channel.Provider, protector, now); if (channel.IsDefault) await db.Channels.Where(item => item.IsDefault).ExecuteUpdateAsync(setters => setters.SetProperty(item => item.IsDefault, false), ct); db.Channels.Add(channel); try { await db.SaveChangesAsync(ct); } catch (DbUpdateException exception) when (IsUniqueViolation(exception)) { return Error(StatusCodes.Status409Conflict, "channel_exists", "A channel with this address already exists."); }
        return TypedResults.Created(CommunicationPath(http, $"/channels/{channel.Id}"), ToChannelResponse(channel));
    }
    private static async Task<IResult> UpdateChannel(Guid id, UpdateChannelRequest? request, CommunicationsDbContext db, MailboxCredentialProtector protector, CancellationToken ct)
    {
        var errors = CommunicationValidation.ValidateChannelUpdate(request); if (errors.Count > 0) return ValidationError(errors);
        var channel = await db.Channels.Include(item => item.Credential).SingleOrDefaultAsync(item => item.Id == id, ct); if (channel is null) return TypedResults.NotFound();
        if (request!.DisplayName is not null) channel.DisplayName = string.IsNullOrEmpty(request.DisplayName) ? null : request.DisplayName;
        if (request.IsActive.HasValue) channel.IsActive = request.IsActive.Value;
        if (request.IsDefault == true) channel.IsDefault = true;
        if (request.Provider is not null || request.Smtp is not null || request.Mailgun is not null)
        {
            var provider = CommunicationValidation.ProviderName(request.Provider ?? (request.Mailgun is not null ? "mailgun" : request.Smtp is not null ? "smtp" : channel.Provider));
            if (!TryUpdateCredential(channel, request.Smtp, request.Mailgun, provider, protector, DateTimeOffset.UtcNow, out var credentialError))
                return ValidationError(new Dictionary<string, string[]> { [provider] = [credentialError!] });
            channel.Provider = provider;
        }
        if (channel.IsDefault) await db.Channels.Where(item => item.Id != id && item.IsDefault).ExecuteUpdateAsync(setters => setters.SetProperty(item => item.IsDefault, false), ct);
        await db.SaveChangesAsync(ct); return TypedResults.Ok(ToChannelResponse(channel));
    }
    private static async Task<IResult> VerifyChannel(Guid id, CommunicationsDbContext db, SmtpDeliveryProvider smtp, MailgunDeliveryProvider mailgun, CancellationToken ct) { var channel = await db.Channels.Include(item => item.Credential).SingleOrDefaultAsync(item => item.Id == id, ct); if (channel is null) return TypedResults.NotFound(); try { using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct); timeout.CancelAfter(TimeSpan.FromSeconds(10)); if (channel.Provider == "smtp") await smtp.VerifyAsync(channel, timeout.Token); else await mailgun.VerifyAsync(channel, timeout.Token); return TypedResults.Ok(new { ok = true }); } catch (Exception) when (!ct.IsCancellationRequested) { return Error(StatusCodes.Status422UnprocessableEntity, "verification_failed", "Channel verification failed."); } }

    private static ChannelResponse ToChannelResponse(Channel item) { ChannelSettingsSummary? settings = null; if (item.Credential is not null && item.Provider == "smtp") { var value = JsonSerializer.Deserialize<SmtpProviderSettings>(item.Credential.SettingsJson, SmtpDeliveryProvider.JsonOptions); if (value is not null) settings = new(value.Host, value.Port, value.UseSsl, value.Username, null, null); } else if (item.Credential is not null && item.Provider == "mailgun") { var value = JsonSerializer.Deserialize<MailgunProviderSettings>(item.Credential.SettingsJson, SmtpDeliveryProvider.JsonOptions); if (value is not null) settings = new(null, null, null, null, value.Domain, value.Region); } return new(item.Id, item.Type, item.Address, item.DisplayName, item.CreatedAt, item.IsActive, item.Provider, item.IsDefault, item.Credential is not null, settings); }
    private static void AddCredential(Channel channel, SmtpChannelCredentialRequest? smtp, MailgunChannelCredentialRequest? mailgun, string provider, MailboxCredentialProtector protector, DateTimeOffset now) { if (provider == "smtp" && smtp is not null) channel.Credential = new ChannelCredential { Id = Guid.NewGuid(), ChannelId = channel.Id, SettingsJson = JsonSerializer.Serialize(new SmtpProviderSettings(smtp.Host!.Trim(), smtp.Port!.Value, smtp.UseSsl ?? false, string.IsNullOrWhiteSpace(smtp.Username) ? null : smtp.Username), SmtpDeliveryProvider.JsonOptions), SecretCiphertext = protector.Protect(smtp.Password ?? string.Empty), CreatedAt = now }; else if (provider == "mailgun" && mailgun is not null) channel.Credential = new ChannelCredential { Id = Guid.NewGuid(), ChannelId = channel.Id, SettingsJson = JsonSerializer.Serialize(new MailgunProviderSettings(mailgun.Domain!.Trim(), mailgun.Region!.Trim().ToLowerInvariant()), SmtpDeliveryProvider.JsonOptions), SecretCiphertext = protector.Protect(JsonSerializer.Serialize(new MailgunCredentialSecrets(mailgun.ApiKey!.Trim(), mailgun.InboundSigningKey?.Trim()), SmtpDeliveryProvider.JsonOptions)), CreatedAt = now }; }
    private static bool TryUpdateCredential(Channel channel, SmtpChannelCredentialRequest? smtp, MailgunChannelCredentialRequest? mailgun, string provider,
        MailboxCredentialProtector protector, DateTimeOffset now, out string? error)
    {
        error = null;
        if (provider == "smtp" && smtp is not null)
        {
            string? existingPassword = null;
            if (channel.Credential is not null)
            {
                try { existingPassword = protector.Unprotect(channel.Credential.SecretCiphertext); } catch (Exception) { }
            }
            var credential = channel.Credential ?? new ChannelCredential { Id = Guid.NewGuid(), ChannelId = channel.Id, SettingsJson = string.Empty, SecretCiphertext = string.Empty, CreatedAt = now };
            credential.SettingsJson = JsonSerializer.Serialize(new SmtpProviderSettings(smtp.Host!.Trim(), smtp.Port!.Value, smtp.UseSsl ?? false, string.IsNullOrWhiteSpace(smtp.Username) ? null : smtp.Username), SmtpDeliveryProvider.JsonOptions);
            credential.SecretCiphertext = protector.Protect(smtp.Password ?? existingPassword ?? string.Empty);
            credential.CreatedAt = now;
            if (channel.Credential is null) channel.Credential = credential;
            return true;
        }
        if (provider == "mailgun" && mailgun is not null)
        {
            MailgunCredentialSecrets? existing = null;
            if (channel.Credential is not null)
            {
                try { existing = MailgunCredentialSecretReader.Read(protector, channel.Credential); } catch (Exception) { }
            }
            var apiKey = string.IsNullOrWhiteSpace(mailgun.ApiKey) ? existing?.ApiKey : mailgun.ApiKey.Trim();
            var signingKey = string.IsNullOrWhiteSpace(mailgun.InboundSigningKey) ? existing?.InboundSigningKey : mailgun.InboundSigningKey.Trim();
            if (string.IsNullOrWhiteSpace(apiKey) || string.IsNullOrWhiteSpace(signingKey))
            {
                error = "Mailgun API key and inbound signing key are required when no existing protected secret is available.";
                return false;
            }
            var credential = channel.Credential ?? new ChannelCredential { Id = Guid.NewGuid(), ChannelId = channel.Id, SettingsJson = string.Empty, SecretCiphertext = string.Empty, CreatedAt = now };
            credential.SettingsJson = JsonSerializer.Serialize(new MailgunProviderSettings(mailgun.Domain!.Trim(), mailgun.Region!.Trim().ToLowerInvariant()), SmtpDeliveryProvider.JsonOptions);
            credential.SecretCiphertext = protector.Protect(JsonSerializer.Serialize(new MailgunCredentialSecrets(apiKey.Trim(), signingKey), SmtpDeliveryProvider.JsonOptions));
            credential.CreatedAt = now;
            if (channel.Credential is null) channel.Credential = credential;
            return true;
        }
        error = "Credentials for the selected provider are required.";
        return false;
    }
}