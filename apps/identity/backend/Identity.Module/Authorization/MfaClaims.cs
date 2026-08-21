using System.Security.Claims;

namespace Vantigo.Identity.Authorization;

/// <summary>
/// Single definition of the "amr"/"mfa" claim pair that marks a cookie session
/// as having completed a second authentication factor. Consolidated here after
/// https://github.com/vantigo-io/vantigo/issues/15 found the identical
/// predicate copied across authorization handlers, the cookie principal
/// refresh, and several endpoints: precisely the kind of duplication that lets
/// a requirement change land in some places and silently miss others.
/// </summary>
public static class MfaClaims
{
    private const string AmrClaimType = "amr";
    private const string MfaMethod = "mfa";

    /// <summary>Whether <paramref name="claim"/> asserts the "mfa" authentication method.</summary>
    public static bool Is(Claim claim) =>
        (claim.Type == AmrClaimType || claim.Type == ClaimTypes.AuthenticationMethod) &&
        string.Equals(claim.Value, MfaMethod, StringComparison.OrdinalIgnoreCase);

    /// <summary>Whether any claim in <paramref name="claims"/> asserts the "mfa" authentication method.</summary>
    public static bool Any(IEnumerable<Claim> claims) => claims.Any(Is);

    /// <summary>The claim pair a fresh MFA-verified sign-in carries.</summary>
    public static IReadOnlyList<Claim> Issue() =>
        [new Claim(AmrClaimType, MfaMethod), new Claim(ClaimTypes.AuthenticationMethod, MfaMethod)];
}