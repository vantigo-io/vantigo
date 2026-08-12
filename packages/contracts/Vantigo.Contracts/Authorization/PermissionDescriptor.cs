namespace Vantigo.Contracts.Authorization;

/// <summary>
/// Immutable, code-owned metadata for one installation permission.
/// Permission keys are stable API identifiers and must not be supplied by an
/// administrator or read from the database as authority.
/// </summary>
public sealed record PermissionDescriptor(
    string Key,
    string DisplayName,
    string Description,
    string Module,
    string Category,
    bool Sensitive = false,
    bool Delegable = true)
{
    public static bool IsValidKey(string? key)
    {
        if (string.IsNullOrWhiteSpace(key)) return false;

        var parts = key.Split(':');
        return parts.Length == 2 &&
            IsIdentifier(parts[0], allowDash: false) &&
            IsIdentifier(parts[1], allowDash: true) &&
            string.Equals(key, key.Trim(), StringComparison.Ordinal);
    }

    public static bool IsValidModule(string? module) =>
        !string.IsNullOrWhiteSpace(module) &&
        module.All(character => character is >= 'a' and <= 'z' or >= '0' and <= '9');

    private static bool IsIdentifier(string value, bool allowDash)
    {
        if (value.Length == 0) return false;
        foreach (var character in value)
        {
            if (character is >= 'a' and <= 'z' || character is >= '0' and <= '9' ||
                allowDash && (character is '_' or '-'))
            {
                continue;
            }

            return false;
        }

        return true;
    }
}