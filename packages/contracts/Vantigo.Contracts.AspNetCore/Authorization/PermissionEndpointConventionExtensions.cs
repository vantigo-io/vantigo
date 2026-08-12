using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Routing;

using Vantigo.Contracts.Authorization;

namespace Vantigo.Contracts.AspNetCore.Authorization;

public static class PermissionEndpointConventionExtensions
{
    public sealed class PermissionMetadata(string permissionKey)
    {
        public string PermissionKey { get; } = permissionKey;
    }

    public static TBuilder RequirePermission<TBuilder>(this TBuilder builder, string permissionKey)
        where TBuilder : IEndpointConventionBuilder
    {
        ArgumentNullException.ThrowIfNull(builder);
        builder.RequireAuthorization(PermissionPolicyName(permissionKey));
        builder.WithMetadata(new PermissionMetadata(permissionKey));
        return builder;
    }

    public static string PermissionPolicyName(string permissionKey) =>
        PermissionDescriptor.IsValidKey(permissionKey)
            ? "permission:" + permissionKey
            : throw new ArgumentException("Permission keys must be lowercase module:verb identifiers.", nameof(permissionKey));

    public static void ValidatePermissionCatalog(
        this IEndpointRouteBuilder endpoints,
        IPermissionCatalog catalog)
    {
        ArgumentNullException.ThrowIfNull(endpoints);
        ArgumentNullException.ThrowIfNull(catalog);

        var keys = endpoints.DataSources
            .SelectMany(source => source.Endpoints)
            .SelectMany(endpoint => endpoint.Metadata.GetOrderedMetadata<PermissionMetadata>())
            .Select(metadata => metadata.PermissionKey)
            .Distinct(StringComparer.Ordinal)
            .ToArray();
        var missing = keys.Where(key => !catalog.Contains(key)).OrderBy(key => key, StringComparer.Ordinal).ToArray();
        if (missing.Length > 0)
            throw new InvalidOperationException(
                "Endpoint permissions are not registered in the composed catalog: " + string.Join(", ", missing));
    }
}