using System;

using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Vantigo.Identity.Database.Accounts.Migrations
{
    /// <inheritdoc />
    public partial class ProfileAvatarsAndMfaSecurity : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            // One-way security cleanup: historical MFA claims were persisted by
            // an older implementation. Down intentionally does not restore
            // these insecure claims.
            migrationBuilder.Sql("""
                DELETE FROM identity.user_claims
                WHERE LOWER(claim_value) = 'mfa'
                  AND LOWER(claim_type) IN (
                      'amr',
                      'authenticationmethod',
                      'authentication_method',
                      'http://schemas.microsoft.com/ws/2008/06/identity/claims/authenticationmethod');
                """);

            migrationBuilder.CreateTable(
                name: "profile_avatars",
                schema: "identity",
                columns: table => new
                {
                    user_id = table.Column<Guid>(type: "uuid", nullable: false),
                    data = table.Column<byte[]>(type: "bytea", nullable: false),
                    content_type = table.Column<string>(type: "character varying(32)", maxLength: 32, nullable: false),
                    updated_at = table.Column<DateTimeOffset>(type: "timestamp with time zone", nullable: false),
                    version = table.Column<long>(type: "bigint", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("pk_profile_avatars", x => x.user_id);
                    table.ForeignKey(
                        name: "fk_profile_avatars_users_user_id",
                        column: x => x.user_id,
                        principalSchema: "identity",
                        principalTable: "users",
                        principalColumn: "id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.Sql("""
                INSERT INTO identity.profile_avatars (user_id, data, content_type, updated_at, version)
                SELECT id, avatar_data, avatar_content_type, CURRENT_TIMESTAMP, 1
                FROM identity.users
                WHERE avatar_data IS NOT NULL
                  AND avatar_content_type IN ('image/png', 'image/jpeg');
                """);

            migrationBuilder.DropColumn(
                name: "avatar_content_type",
                schema: "identity",
                table: "users");

            migrationBuilder.DropColumn(
                name: "avatar_data",
                schema: "identity",
                table: "users");

        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<string>(
                name: "avatar_content_type",
                schema: "identity",
                table: "users",
                type: "character varying(64)",
                maxLength: 64,
                nullable: true);

            migrationBuilder.AddColumn<byte[]>(
                name: "avatar_data",
                schema: "identity",
                table: "users",
                type: "bytea",
                nullable: true);

            migrationBuilder.Sql("""
                UPDATE identity.users AS users
                SET avatar_data = avatars.data,
                    avatar_content_type = avatars.content_type
                FROM identity.profile_avatars AS avatars
                WHERE users.id = avatars.user_id;
                """);

            migrationBuilder.DropTable(
                name: "profile_avatars",
                schema: "identity");
        }
    }
}