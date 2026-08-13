namespace Vantigo.Identity.Database.Accounts;

public enum FederationProviderKind
{
    Entra,
    Google,
    Generic
}

public enum JitCreationMode
{
    Disabled,
    CreateUser
}

public enum FederationConnectionValidationState
{
    Draft,
    Succeeded,
    Failed
}

/// <summary>
/// Deployment-scoped OIDC connection configuration. Client secret material is
/// never persisted; ClientSecretReference names a deployment configuration value
/// resolved only at runtime.
/// </summary>
public sealed class FederationConnection
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public FederationProviderKind ProviderKind { get; set; }
    public required string DisplayName { get; set; }
    public required string Authority { get; set; }
    public required string ClientId { get; set; }
    public string? ClientSecretReference { get; set; }
    public string[] AllowedDomains { get; set; } = [];
    public bool IsEnabled { get; set; }
    public bool IsDefault { get; set; }
    public JitCreationMode JitCreationMode { get; set; }
    public int ConfigurationVersion { get; set; } = 1;
    public FederationConnectionValidationState ValidationState { get; set; } = FederationConnectionValidationState.Draft;
    public string? ValidationErrorCode { get; set; }
    public DateTimeOffset? ValidationCompletedAt { get; set; }
    public int? ValidatedConfigurationVersion { get; set; }
    public string? ValidatedIssuer { get; set; }
    public string? ValidatedDiscoveryEndpoint { get; set; }
    public string? ValidatedAuthorizationEndpoint { get; set; }
    public string? ValidatedTokenEndpoint { get; set; }
    public string? ValidatedJwksUri { get; set; }
    public required string ConcurrencyStamp { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
}