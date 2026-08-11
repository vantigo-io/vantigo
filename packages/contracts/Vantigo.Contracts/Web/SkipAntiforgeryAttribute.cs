namespace Vantigo.Contracts.Web;

/// <summary>
/// Marker metadata that tells the host antiforgery middleware to skip validation
/// for the decorated endpoint. Use only for endpoints that are legitimately
/// exempt from same-origin CSRF protection (e.g. external webhooks or callbacks).
/// </summary>
[AttributeUsage(AttributeTargets.Class | AttributeTargets.Method | AttributeTargets.Delegate)]
public sealed class SkipAntiforgeryAttribute : Attribute;