using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Host.Security;

/// <summary>
/// Browser security headers applied to every response the host produces, API
/// responses included.
/// </summary>
/// <remarks>
/// The content security policy is written against the actual published frontend:
/// Vite emits a single external module entry plus stylesheet links, and the only
/// inline script in the served document is the runtime configuration
/// <see cref="SpaIndexDocument"/> injects, allowed here by the SHA-256 hash that
/// document computes from the very same string. <c>style-src</c> keeps
/// <c>'unsafe-inline'</c> because Mantine renders its CSS-variable and inline
/// style blocks as inline <c>&lt;style&gt;</c> elements and React style props as
/// style attributes; the published bundle contains no <c>eval</c>,
/// <c>new Function</c>, worker or blob-URL usage, so <c>script-src</c> needs
/// neither <c>'unsafe-eval'</c> nor <c>blob:</c>.
/// </remarks>
public static class SecurityHeaders
{
    /// <summary>Header name used when the policy is enforced.</summary>
    public const string ContentSecurityPolicyHeaderName = "Content-Security-Policy";

    /// <summary>Header name used when the policy is only reported.</summary>
    public const string ContentSecurityPolicyReportOnlyHeaderName = "Content-Security-Policy-Report-Only";

    /// <summary>
    /// Referrer policy. The application is an internal back office; no outbound
    /// link needs to disclose the page a user came from.
    /// </summary>
    public const string ReferrerPolicy = "no-referrer";

    /// <summary>
    /// Powerful features the application never uses, denied for the document and
    /// for anything it embeds. Features that are left unlisted keep their browser
    /// default allowlist.
    /// </summary>
    public const string PermissionsPolicy =
        "accelerometer=(), autoplay=(), browsing-topics=(), camera=(), display-capture=(), " +
        "encrypted-media=(), fullscreen=(self), geolocation=(), gyroscope=(), " +
        "idle-detection=(), local-fonts=(), magnetometer=(), microphone=(), midi=(), " +
        "payment=(), picture-in-picture=(), screen-wake-lock=(), serial=(), usb=(), " +
        "xr-spatial-tracking=()";

    /// <summary>
    /// Builds the content security policy allowing <paramref name="inlineScriptHash"/>
    /// as the only inline script.
    /// </summary>
    public static string BuildContentSecurityPolicy(string inlineScriptHash) =>
        string.Join(
            "; ",
            "default-src 'self'",
            "base-uri 'self'",
            "object-src 'none'",
            "frame-ancestors 'none'",
            "form-action 'self'",
            $"script-src 'self' {inlineScriptHash}",
            // Mantine injects its theme CSS variables and component styles as
            // inline <style> elements, and React style props render as style
            // attributes; both are inline styles as far as CSP is concerned.
            "style-src 'self' 'unsafe-inline'",
            // data: covers the SVG chevrons Mantine inlines into its stylesheet,
            // https: the configurable App:LogoUrl and remote images inside the
            // sandboxed HTML email preview.
            "img-src 'self' data: blob: https:",
            "font-src 'self' data:",
            "connect-src 'self'",
            "frame-src 'self'",
            "worker-src 'self'",
            "manifest-src 'self'");

    /// <summary>
    /// Writes the security headers on every response. Place immediately after the
    /// exception handler: the headers are attached when the response starts, so
    /// they survive the response reset a re-executed error page performs.
    /// </summary>
    public static IApplicationBuilder UseVantigoSecurityHeaders(this WebApplication app)
    {
        SpaIndexDocument index = app.Services.GetRequiredService<SpaIndexDocument>();
        TransportSecurityOptions transportSecurity =
            app.Services.GetRequiredService<IOptions<TransportSecurityOptions>>().Value;

        string policyHeaderName = transportSecurity.ContentSecurityPolicyReportOnly
            ? ContentSecurityPolicyReportOnlyHeaderName
            : ContentSecurityPolicyHeaderName;
        string policy = BuildContentSecurityPolicy(index.InlineScriptSha256);

        return app.Use((context, next) =>
        {
            context.Response.OnStarting(ApplyAsync, new ResponseHeaderState(context.Response, policyHeaderName, policy));
            return next(context);
        });
    }

    private static Task ApplyAsync(object state)
    {
        ResponseHeaderState headers = (ResponseHeaderState)state;
        IHeaderDictionary target = headers.Response.Headers;
        target[headers.PolicyHeaderName] = headers.Policy;
        target.XContentTypeOptions = "nosniff";
        target.XFrameOptions = "DENY";
        target["Referrer-Policy"] = ReferrerPolicy;
        target["Permissions-Policy"] = PermissionsPolicy;
        return Task.CompletedTask;
    }

    private sealed record ResponseHeaderState(HttpResponse Response, string PolicyHeaderName, string Policy);
}