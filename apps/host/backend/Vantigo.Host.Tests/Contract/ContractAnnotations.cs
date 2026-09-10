using System.Collections.Concurrent;
using System.Globalization;
using System.Text;

using Microsoft.AspNetCore.Authorization;

using Vantigo.Contracts.AspNetCore.Authorization;

namespace Vantigo.Host.Tests.Contract;

/// <summary>
/// The rules that turn .NET endpoint metadata into the Go contract's
/// x-vantigo-access and operationId values. Part of the extraction harness for
/// the Go port; removed with the .NET host at the cutover.
/// </summary>
public static class ContractAnnotations
{
    private const string ScimPrefix = "api/v1/identity/scim/v2";
    private const string PermissionPolicyPrefix = "permission:";

    /// <summary>
    /// anonymous | session | scim | policy:A[+B] | permission:module:verb[+module:verb].
    /// SCIM endpoints authenticate with a static bearer token, not a session,
    /// whatever their metadata says. Endpoints with no authorization metadata
    /// at all are public (the host sets no fallback policy).
    /// </summary>
    public static string Access(IReadOnlyList<object> metadata, string relativePath)
    {
        if (relativePath.StartsWith(ScimPrefix, StringComparison.Ordinal))
        {
            return "scim";
        }
        if (metadata.OfType<IAllowAnonymous>().Any())
        {
            return "anonymous";
        }

        var authorize = metadata.OfType<IAuthorizeData>().ToList();

        var permissionKeys = metadata
            .OfType<PermissionEndpointConventionExtensions.PermissionMetadata>()
            .Select(permission => permission.PermissionKey)
            .Distinct(StringComparer.Ordinal)
            .Order(StringComparer.Ordinal)
            .ToList();
        if (permissionKeys.Count > 0)
        {
            var nonPermissionPolicy = authorize
                .Select(a => a.Policy)
                .FirstOrDefault(p => !string.IsNullOrEmpty(p) && !p.StartsWith(PermissionPolicyPrefix, StringComparison.Ordinal));
            if (nonPermissionPolicy is not null)
            {
                throw new InvalidOperationException(
                    $"Endpoint '{relativePath}' has permission metadata and the non-permission authorization policy " +
                    $"'{nonPermissionPolicy}'; the permission:module:verb[+module:verb] grammar cannot express both.");
            }
            return "permission:" + string.Join("+", permissionKeys);
        }

        var policies = authorize
            .Select(a => a.Policy)
            .Where(p => !string.IsNullOrEmpty(p) && !p.StartsWith(PermissionPolicyPrefix, StringComparison.Ordinal))
            .Select(p => p!)
            .Distinct(StringComparer.Ordinal)
            .Order(StringComparer.Ordinal)
            .ToList();
        if (policies.Count > 0)
        {
            return "policy:" + string.Join("+", policies);
        }

        return authorize.Count > 0 ? "session" : "anonymous";
    }

    /// <summary>
    /// The endpoint's name in camelCase when it has one; otherwise the HTTP
    /// method followed by the PascalCase path segments after api/v1, with each
    /// route parameter rendered as By{Name} and route constraints dropped.
    /// </summary>
    public static string OperationId(string httpMethod, string relativePath, string? endpointName)
    {
        if (!string.IsNullOrEmpty(endpointName))
        {
            return char.ToLowerInvariant(endpointName[0]) + endpointName[1..];
        }

        var builder = new StringBuilder(httpMethod.ToLowerInvariant());
        foreach (var segment in relativePath.Split('/', StringSplitOptions.RemoveEmptyEntries).SkipWhile(s => s is "api" or "v1"))
        {
            if (segment.StartsWith('{'))
            {
                var name = segment.Trim('{', '}').Split(':')[0];
                builder.Append("By").Append(Pascal(name));
            }
            else
            {
                builder.Append(Pascal(segment));
            }
        }
        return builder.ToString();
    }

    public static void EnsureUniqueOperationIds(IEnumerable<string> operationIds)
    {
        var duplicates = operationIds
            .GroupBy(id => id, StringComparer.Ordinal)
            .Where(group => group.Count() > 1)
            .Select(group => group.Key)
            .Order(StringComparer.Ordinal)
            .ToList();
        if (duplicates.Count > 0)
        {
            throw new InvalidOperationException("Duplicate operationIds: " + string.Join(", ", duplicates));
        }
    }

    private static string Pascal(string segment) => string.Concat(
        segment.Split('-', '_', '.')
            .Where(part => part.Length > 0)
            .Select(part => char.ToUpper(part[0], CultureInfo.InvariantCulture) + part[1..]));

    /// <summary>
    /// The component schema id for <paramref name="type"/>. Nested endpoint-local
    /// types (nearly every *Request/*Response DTO) get their declaring types'
    /// names, outermost first and with a trailing Endpoint(s) stripped, prefixed
    /// onto <paramref name="defaultId"/> so that e.g. AttachCustomerContactEndpoint.Request
    /// and CustomersEndpoints.Request don't collapse onto one "Request" schema.
    /// If the resulting id still collides with another type's (per
    /// <paramref name="ambiguousIds"/>), it is further prefixed with the type's
    /// module (its assembly name, with a leading "Vantigo." and any "." removed).
    /// </summary>
    public static string SchemaId(Type type, string defaultId, IReadOnlySet<string> ambiguousIds)
    {
        var id = defaultId;
        if (type.IsNested)
        {
            var declaringNames = new List<string>();
            for (var declaring = type.DeclaringType; declaring is not null; declaring = declaring.DeclaringType)
            {
                declaringNames.Add(StripEndpointSuffix(declaring.Name));
            }
            declaringNames.Reverse();
            id = string.Concat(declaringNames) + defaultId;
        }

        if (ambiguousIds.Contains(id))
        {
            id = ModuleName(type) + id;
        }

        return id;
    }

    /// <summary>
    /// The schema ids from <paramref name="types"/> (computed with no ambiguous
    /// ids, i.e. before any module prefix) that more than one distinct type
    /// produces. Passing this to <see cref="SchemaId"/> is what makes the module
    /// prefix depend only on the set of types being documented, never on the
    /// order the OpenAPI generator happens to meet them in.
    /// </summary>
    public static IReadOnlySet<string> AmbiguousSchemaIds(IEnumerable<Type> types) => types
        .Select(type => (Type: type, Id: SchemaId(type, type.Name, ImmutableEmptyIds)))
        .GroupBy(pair => pair.Id, pair => pair.Type, StringComparer.Ordinal)
        .Where(group => group.Distinct().Count() > 1)
        .Select(group => group.Key)
        .ToHashSet();

    private static readonly IReadOnlySet<string> ImmutableEmptyIds = new HashSet<string>();

    private static string StripEndpointSuffix(string name)
    {
        if (name.EndsWith("Endpoints", StringComparison.Ordinal))
        {
            return name[..^"Endpoints".Length];
        }
        if (name.EndsWith("Endpoint", StringComparison.Ordinal))
        {
            return name[..^"Endpoint".Length];
        }
        return name;
    }

    private static string ModuleName(Type type) => (type.Assembly.GetName().Name ?? string.Empty)
        .Replace("Vantigo.", string.Empty, StringComparison.Ordinal)
        .Replace(".", string.Empty, StringComparison.Ordinal);

    /// <summary>
    /// Guards against two distinct types claiming the same schema id — the
    /// failure mode this whole file exists to prevent. Thread-safe because the
    /// OpenAPI generator's CreateSchemaReferenceId callback can run concurrently.
    /// </summary>
    public sealed class SchemaIdRegistry
    {
        private readonly ConcurrentDictionary<string, Type> _claims = new(StringComparer.Ordinal);

        public void Claim(Type type, string id)
        {
            var normalized = Nullable.GetUnderlyingType(type) ?? type;
            var owner = _claims.GetOrAdd(id, normalized);
            if (owner != normalized)
            {
                throw new InvalidOperationException(
                    $"Schema id '{id}' is claimed by {owner.FullName} and {normalized.FullName}");
            }
        }
    }
}