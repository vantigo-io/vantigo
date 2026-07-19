import { describe, expect, it } from "vitest";
import { summarizeUserAgent } from "./user-agent";

const CHROME_MAC =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36";
const SAFARI_IPHONE =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1";
const FIREFOX_WINDOWS =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:127.0) Gecko/20100101 Firefox/127.0";
const EDGE_WINDOWS =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0";

describe("summarizeUserAgent", () => {
  it("handles missing user agents", () => {
    expect(summarizeUserAgent(null)).toEqual({ browser: null, os: null });
    expect(summarizeUserAgent("")).toEqual({ browser: null, os: null });
  });

  it("detects Chrome on macOS", () => {
    expect(summarizeUserAgent(CHROME_MAC)).toEqual({ browser: "Chrome", os: "macOS" });
  });

  it("detects Safari on iOS", () => {
    expect(summarizeUserAgent(SAFARI_IPHONE)).toEqual({ browser: "Safari", os: "iOS" });
  });

  it("detects Firefox on Windows", () => {
    expect(summarizeUserAgent(FIREFOX_WINDOWS)).toEqual({ browser: "Firefox", os: "Windows" });
  });

  it("detects Edge (not Chrome) on Windows", () => {
    expect(summarizeUserAgent(EDGE_WINDOWS)).toEqual({ browser: "Edge", os: "Windows" });
  });
});
