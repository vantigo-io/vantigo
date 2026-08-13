using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class FederationConnectionEntityTypeConfiguration : IEntityTypeConfiguration<FederationConnection>
{
    public void Configure(EntityTypeBuilder<FederationConnection> builder)
    {
        builder.ToTable("federation_connections", "identity");
        builder.HasKey(connection => connection.Id).HasName("pk_federation_connections");
        builder.Property(connection => connection.Id).HasColumnName("id");
        builder.Property(connection => connection.ProviderKind).HasColumnName("provider_kind").HasConversion<string>().HasMaxLength(20).IsRequired();
        builder.Property(connection => connection.DisplayName).HasColumnName("display_name").HasMaxLength(200).IsRequired();
        builder.Property(connection => connection.Authority).HasColumnName("authority").HasMaxLength(2048).IsRequired();
        builder.Property(connection => connection.ClientId).HasColumnName("client_id").HasMaxLength(256).IsRequired();
        builder.Property(connection => connection.ClientSecretReference).HasColumnName("client_secret_reference").HasMaxLength(2048);
        builder.Property(connection => connection.AllowedDomains).HasColumnName("allowed_domains").HasColumnType("text[]").IsRequired();
        builder.Property(connection => connection.IsEnabled).HasColumnName("is_enabled").IsRequired();
        builder.Property(connection => connection.IsDefault).HasColumnName("is_default").IsRequired();
        builder.Property(connection => connection.JitCreationMode).HasColumnName("jit_creation_mode").HasConversion<string>().HasMaxLength(32).IsRequired();
        builder.Property(connection => connection.ConfigurationVersion).HasColumnName("configuration_version").IsRequired();
        builder.Property(connection => connection.ValidationState).HasColumnName("validation_state").HasConversion<string>().HasMaxLength(20).IsRequired();
        builder.Property(connection => connection.ValidationErrorCode).HasColumnName("validation_error_code").HasMaxLength(100);
        builder.Property(connection => connection.ValidationCompletedAt).HasColumnName("validation_completed_at");
        builder.Property(connection => connection.ValidatedConfigurationVersion).HasColumnName("validated_configuration_version");
        builder.Property(connection => connection.ValidatedIssuer).HasColumnName("validated_issuer").HasMaxLength(2048);
        builder.Property(connection => connection.ValidatedDiscoveryEndpoint).HasColumnName("validated_discovery_endpoint").HasMaxLength(2048);
        builder.Property(connection => connection.ValidatedAuthorizationEndpoint).HasColumnName("validated_authorization_endpoint").HasMaxLength(2048);
        builder.Property(connection => connection.ValidatedTokenEndpoint).HasColumnName("validated_token_endpoint").HasMaxLength(2048);
        builder.Property(connection => connection.ValidatedJwksUri).HasColumnName("validated_jwks_uri").HasMaxLength(2048);
        builder.Property(connection => connection.ConcurrencyStamp).HasColumnName("concurrency_stamp").IsConcurrencyToken().IsRequired();
        builder.Property(connection => connection.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(connection => connection.UpdatedAt).HasColumnName("updated_at").IsRequired();
        builder.HasIndex(connection => connection.DisplayName).IsUnique().HasDatabaseName("ux_federation_connections_display_name");
        builder.HasIndex(connection => connection.IsDefault).IsUnique().HasFilter("is_default = TRUE").HasDatabaseName("ux_federation_connections_default");
    }
}