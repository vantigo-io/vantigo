export interface UserAgentSummary {
  browser: string | null;
  os: string | null;
}

/** Tiny, dependency-free user-agent summary — good enough for a session list. */
export function summarizeUserAgent(userAgent: string | null | undefined): UserAgentSummary {
  if (!userAgent) return { browser: null, os: null };

  const os = userAgent.includes("Windows")
    ? "Windows"
    : userAgent.includes("Android")
      ? "Android"
      : userAgent.includes("iPhone") || userAgent.includes("iPad")
        ? "iOS"
        : userAgent.includes("Mac OS X")
          ? "macOS"
          : userAgent.includes("Linux")
            ? "Linux"
            : null;

  const browser = userAgent.includes("Edg/")
    ? "Edge"
    : userAgent.includes("OPR/")
      ? "Opera"
      : userAgent.includes("Firefox/")
        ? "Firefox"
        : userAgent.includes("Chrome/")
          ? "Chrome"
          : userAgent.includes("Safari/")
            ? "Safari"
            : null;

  return { browser, os };
}
