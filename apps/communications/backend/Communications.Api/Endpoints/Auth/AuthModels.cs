using System.Text.Json.Serialization;

namespace Vantigo.Communications.Api.Endpoints.Auth;

internal static class AuthRoles
{
    internal const string Owner = "Owner";
    internal const string User = "User";
}

internal static class AuthPolicies
{
    internal const string Owner = "Owner";
    internal const string Business = "Business";
}

internal sealed record BootstrapRequest(string? Secret, string? Email, string? DisplayName, string? Password);
internal sealed record LoginRequest(string? Email, string? Password);
internal sealed record AuthUserResponse(Guid Id, string DisplayName, string? Email, IReadOnlyCollection<string> Roles);
internal sealed record AuthSessionResponse(AuthUserResponse User);
internal sealed record AuthSuccessResponse(AuthUserResponse? User);
internal sealed record LogoutResponse(bool Success);
internal sealed record BootstrapStatusResponse(bool Available);
internal sealed record AntiforgeryResponse([property: JsonPropertyName("token")] string Token);
internal sealed record AuthErrorResponse(AuthError Error);
internal sealed record AuthError(string Code, string Message, IReadOnlyDictionary<string, string[]>? Fields = null);
