using System.Text.Json.Serialization;

namespace Vantigo.Identity.Endpoints.Auth;

internal static class AuthRateLimitPolicies
{
    internal const string Login = "auth-login";
    internal const string Bootstrap = "auth-bootstrap";
    internal const string Invitations = "auth-invitations";
    internal const string InvitationAcceptance = "auth-invitation-acceptance";
    internal const string PasswordRecovery = "auth-password-recovery";
    internal const string Mfa = "auth-mfa";
    internal const string PasskeyLogin = "auth-passkey-login";
    internal const string UserManagement = "auth-user-management";
}

internal sealed record BootstrapRequest(
    string? Secret,
    string? Email,
    string? DisplayName,
    string? Password);

internal sealed record BootstrapStatusResponse(bool Available);

internal sealed record LoginRequest(string? Email, string? Password);

internal sealed record TwoFactorRequest(string? Code, bool RememberMe = false);

internal sealed record InvitationRequest(string? Email, string? DisplayName, string? Role);

internal sealed record OwnerUserCreateRequest(
    string? DisplayName,
    string? Email,
    string? Role,
    string? Password,
    string? TemporaryPassword = null);

internal sealed record OwnerUserUpdateRequest(string? DisplayName, string? Email, string? Role);

internal sealed record OwnerUserPasswordRequest(string? Password);

internal sealed record InvitationAcceptanceRequest(string? Token, string? DisplayName, string? Password);

internal sealed record PasswordRecoveryRequest(string? Email);

internal sealed record PasswordResetRequest(string? Email, string? Token, string? NewPassword);

internal sealed record MfaCodeRequest(string? Code, string? Password = null);

internal sealed record OwnerMfaResetRequest(Guid UserId);

internal sealed record AuthUserResponse(
    Guid Id,
    string DisplayName,
    string? Email,
    IReadOnlyCollection<string> Roles);

internal sealed record AuthSessionResponse(
    AuthUserResponse User,
    bool TwoFactorEnabled,
    bool MfaEnrollmentRequired,
    bool MfaAuthenticated);

internal sealed record AuthSuccessResponse(
    AuthUserResponse? User,
    bool RequiresTwoFactor,
    bool TwoFactorEnabled,
    bool MfaEnrollmentRequired);

internal sealed record LogoutResponse(bool Success);

internal sealed record AntiforgeryResponse([property: JsonPropertyName("token")] string Token);

internal sealed record OidcProvidersResponse(OidcProviderResponse? Oidc);

internal sealed record OidcProviderResponse(string DisplayName);

internal sealed record InvitationResponse(Guid Id, string Email, string Role, string? DisplayName,
    DateTimeOffset CreatedAt, DateTimeOffset ExpiresAt, DateTimeOffset? RevokedAt, DateTimeOffset? AcceptedAt);

internal sealed record OwnerUserResponse(Guid Id, string DisplayName, string? Email, string Role, bool Active,
    bool Disabled, bool LockedOut, DateTimeOffset? LockoutEnd, bool TwoFactorEnabled);

internal sealed record InvitationAcceptanceResponse(bool Valid, string? Email = null, string? Role = null, DateTimeOffset? ExpiresAt = null);

internal sealed record PasswordRecoveryResponse(bool Accepted);

internal sealed record PasswordResetResponse(bool Success);

internal sealed record MfaStatusResponse(bool TwoFactorEnabled, bool MfaEnrollmentRequired);

internal sealed record MfaSetupResponse(string? SharedKey, string? AuthenticatorUri, bool Initialized);

internal sealed record MfaEnableResponse(bool TwoFactorEnabled, IReadOnlyCollection<string> RecoveryCodes);

internal sealed record MfaRecoveryCodesResponse(IReadOnlyCollection<string> RecoveryCodes);

internal sealed record MfaResetResponse(Guid UserId, IReadOnlyCollection<string> RecoveryCodes);