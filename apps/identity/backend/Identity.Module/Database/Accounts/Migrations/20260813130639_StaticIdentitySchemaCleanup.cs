using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations;

public partial class StaticIdentitySchemaCleanup : Migration
{
    protected override void Up(MigrationBuilder migrationBuilder)
    {
        // Dynamic federation/SCIM was never deployed with durable users. Discard
        // its configuration and provenance while retaining the deterministic
        // static connection, static mappings, local groups, memberships, role
        // mappings, and operational events.
        // Drop the dynamic token child table before deleting any connection
        // principals. This forward migration is intentionally destructive:
        // dynamic federation/SCIM was never deployed with durable users.
        migrationBuilder.DropTable("scim_bearer_tokens", "identity");
        migrationBuilder.Sql("""
            DELETE FROM identity.access_group_role_mappings
            WHERE group_id IN (
                SELECT id FROM identity.access_groups
                WHERE scim_connection_id IS NOT NULL
                  AND scim_connection_id <> '7f6b7f8a-7c7e-4c19-8f06-6c64d3b7f4d2');
            DELETE FROM identity.access_group_memberships
            WHERE group_id IN (
                SELECT id FROM identity.access_groups
                WHERE scim_connection_id IS NOT NULL
                  AND scim_connection_id <> '7f6b7f8a-7c7e-4c19-8f06-6c64d3b7f4d2');
            DELETE FROM identity.access_groups
            WHERE scim_connection_id IS NOT NULL
              AND scim_connection_id <> '7f6b7f8a-7c7e-4c19-8f06-6c64d3b7f4d2';
            DELETE FROM identity.scim_user_mappings
            WHERE scim_connection_id <> '7f6b7f8a-7c7e-4c19-8f06-6c64d3b7f4d2';
            DELETE FROM identity.scim_connections
            WHERE id <> '7f6b7f8a-7c7e-4c19-8f06-6c64d3b7f4d2';
            """);

        migrationBuilder.DropForeignKey("fk_scim_connections_federation_connections_id", "scim_connections", "identity");
        migrationBuilder.DropTable("federated_identities", "identity");
        migrationBuilder.DropTable("federation_oidc_states", "identity");
        migrationBuilder.DropTable("federation_connections", "identity");
        migrationBuilder.DropIndex("ux_scim_connections_federation_connection_id", "scim_connections", "identity");
        migrationBuilder.DropIndex("ux_scim_connections_static", "scim_connections", "identity");

        migrationBuilder.DropColumn("lifecycle_override", "scim_user_mappings", "identity");
        migrationBuilder.DropColumn("lifecycle_override_reason", "scim_user_mappings", "identity");
        migrationBuilder.DropColumn("concurrency_stamp", "scim_connections", "identity");
        migrationBuilder.DropColumn("federation_connection_id", "scim_connections", "identity");
        migrationBuilder.DropColumn("is_enabled", "scim_connections", "identity");
        migrationBuilder.DropColumn("is_static", "scim_connections", "identity");
        migrationBuilder.DropColumn("last_revoked_at", "scim_connections", "identity");
        migrationBuilder.DropColumn("last_rotated_at", "scim_connections", "identity");
        migrationBuilder.DropColumn("mode", "scim_connections", "identity");
        migrationBuilder.DropColumn("token_version", "scim_connections", "identity");
    }

    protected override void Down(MigrationBuilder migrationBuilder) =>
        throw new NotSupportedException("This migration is destructive and cannot be reversed without provenance.");
}