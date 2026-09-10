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
    /// anonymous | session | scim | policy:A[+B] | permission:module:verb.
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

        var permission = metadata.OfType<PermissionEndpointConventionExtensions.PermissionMetadata>().LastOrDefault();
        if (permission is not null)
        {
            return "permission:" + permission.PermissionKey;
        }

        var authorize = metadata.OfType<IAuthorizeData>().ToList();
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
}