namespace Vantigo.Contracts.Authorization;

public interface IPermissionCatalogContributor
{
    void Contribute(PermissionCatalogBuilder catalog);
}

public interface IPermissionCatalog
{
    IReadOnlyList<PermissionDescriptor> Permissions { get; }

    bool Contains(string key);

    PermissionDescriptor GetRequired(string key);
}

/// <summary>Builds the installation catalog during application composition.</summary>
public sealed class PermissionCatalogBuilder
{
    private readonly List<PermissionDescriptor> permissions = [];

    public PermissionCatalogBuilder Add(PermissionDescriptor descriptor)
    {
        ArgumentNullException.ThrowIfNull(descriptor);
        permissions.Add(descriptor);
        return this;
    }

    public PermissionCatalogBuilder AddRange(IEnumerable<PermissionDescriptor> descriptors)
    {
        ArgumentNullException.ThrowIfNull(descriptors);
        foreach (var descriptor in descriptors) Add(descriptor);
        return this;
    }

    public PermissionCatalog Build() => PermissionCatalog.Create(permissions);
}

public sealed class PermissionCatalog : IPermissionCatalog
{
    private readonly IReadOnlyDictionary<string, PermissionDescriptor> byKey;

    private PermissionCatalog(IReadOnlyList<PermissionDescriptor> permissions)
    {
        Permissions = permissions;
        byKey = permissions.ToDictionary(permission => permission.Key, StringComparer.Ordinal);
    }

    public IReadOnlyList<PermissionDescriptor> Permissions { get; }

    public bool Contains(string key) => byKey.ContainsKey(key);

    public PermissionDescriptor GetRequired(string key) => byKey.TryGetValue(key, out var value)
        ? value
        : throw new KeyNotFoundException($"The permission '{key}' is not registered.");

    public static PermissionCatalog Create(IEnumerable<IPermissionCatalogContributor> contributors)
    {
        ArgumentNullException.ThrowIfNull(contributors);
        var builder = new PermissionCatalogBuilder();
        foreach (var contributor in contributors)
        {
            ArgumentNullException.ThrowIfNull(contributor);
            contributor.Contribute(builder);
        }

        return builder.Build();
    }

    public static PermissionCatalog Create(IEnumerable<PermissionDescriptor> descriptors)
    {
        ArgumentNullException.ThrowIfNull(descriptors);
        var values = descriptors.ToArray();
        var errors = new List<string>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var descriptor in values)
        {
            if (!PermissionDescriptor.IsValidKey(descriptor.Key))
                errors.Add($"Permission key '{descriptor.Key}' is malformed; expected lowercase module:verb.");
            if (!seen.Add(descriptor.Key))
                errors.Add($"Permission key '{descriptor.Key}' is registered more than once.");
            if (string.IsNullOrWhiteSpace(descriptor.DisplayName))
                errors.Add($"Permission '{descriptor.Key}' must have a display name.");
            if (string.IsNullOrWhiteSpace(descriptor.Description))
                errors.Add($"Permission '{descriptor.Key}' must have a description.");
            if (!PermissionDescriptor.IsValidModule(descriptor.Module) ||
                !descriptor.Key.StartsWith(descriptor.Module + ":", StringComparison.Ordinal) ||
                string.IsNullOrWhiteSpace(descriptor.Category))
                errors.Add($"Permission '{descriptor.Key}' has invalid module metadata.");
        }

        if (errors.Count > 0)
            throw new InvalidOperationException("The authorization permission catalog is invalid: " + string.Join(" ", errors));

        return new PermissionCatalog(values
            .OrderBy(permission => permission.Key, StringComparer.Ordinal)
            .ToArray());
    }
}