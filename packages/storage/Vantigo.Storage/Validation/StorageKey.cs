using System.Text;
using System.Text.RegularExpressions;

namespace Vantigo.Storage.Validation;

/// <summary>Validates and combines the logical keys used by every storage provider.</summary>
internal static partial class StorageKey
{
    private const int MaximumUtf8Bytes = 1024;

    public static string ValidateScope(string scope)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(scope);
        if (!ScopeRegex().IsMatch(scope) || scope.Length > 64)
            throw new ArgumentException($"'{scope}' is not a safe storage scope.", nameof(scope));
        return scope;
    }

    /// <summary>Validates a relative key and returns it unchanged.</summary>
    public static string ValidateRelativeKey(string key, string? scope = null)
    {
        ValidateKeyCore(key, nameof(key));
        if (scope is not null && (key.Equals(scope, StringComparison.Ordinal) ||
                                  key.StartsWith(scope + "/", StringComparison.Ordinal)))
            throw new ArgumentException("A scoped store accepts relative keys and must not be given its scope prefix.", nameof(key));
        return key;
    }

    /// <summary>Validates a provider key, which may already contain a scope prefix.</summary>
    public static string ValidateProviderKey(string key) => ValidateKeyCore(key, nameof(key));

    public static string Combine(string scope, string relativeKey)
    {
        ValidateScope(scope);
        ValidateRelativeKey(relativeKey, scope);
        var combined = $"{scope}/{relativeKey}";
        if (Encoding.UTF8.GetByteCount(combined) > MaximumUtf8Bytes)
            throw new ArgumentException("The combined storage key is too long.", nameof(relativeKey));
        return combined;
    }

    private static string ValidateKeyCore(string key, string parameterName)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(key);
        if (key.Length > MaximumUtf8Bytes || Encoding.UTF8.GetByteCount(key) > MaximumUtf8Bytes ||
            key[0] is '/' or '\\' || key.Contains('\\') || key.Contains('%') ||
            key.Contains(':') || key.Contains('?') || key.Contains('#') ||
            key.Contains("://", StringComparison.Ordinal) || Path.IsPathRooted(key) ||
            (key.Length >= 2 && char.IsLetter(key[0]) && key[1] == ':') || key.Any(char.IsControl))
            throw new ArgumentException($"'{key}' is not a safe object storage key.", parameterName);

        var segments = key.Split('/', StringSplitOptions.None);
        if (segments.Any(segment => segment.Length == 0 || segment is "." or ".."))
            throw new ArgumentException($"'{key}' is not a safe object storage key.", parameterName);
        return key;
    }

    [GeneratedRegex("^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$", RegexOptions.CultureInvariant)]
    private static partial Regex ScopeRegex();
}