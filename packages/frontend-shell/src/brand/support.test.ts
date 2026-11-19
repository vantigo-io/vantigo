import { describe, expect, it } from "vitest";
import { safeSupportUrl, supportMailto } from "./support";

describe("safeSupportUrl", () => {
  it("accepts http(s) URLs and same-origin paths", () => {
    expect(safeSupportUrl("https://help.example.com/a?b=c")).toBe("https://help.example.com/a?b=c");
    expect(safeSupportUrl("http://example.com")).toBe("http://example.com");
    expect(safeSupportUrl("/help")).toBe("/help");
  });

  it("refuses everything else", () => {
    for (const bad of [
      "javascript:alert(1)",
      "data:text/html,x",
      "//evil.example",
      "/\\evil.example",
      "help/page",
      "ftp://example.com",
      "java\tscript:alert(1)",
      "/a\nb",
      "https://example.com/a\rb",
      "",
    ]) {
      expect(safeSupportUrl(bad), bad).toBeNull();
    }
  });
});

describe("supportMailto", () => {
  it("makes a mailto link for a plain address", () => {
    expect(supportMailto("support@example.com")).toBe("mailto:support@example.com");
    expect(supportMailto("a+b@example.com")).toBe("mailto:a%2Bb@example.com");
  });

  it("refuses an address that could add a header or an escape", () => {
    for (const bad of [
      '"a?cc=x@evil.example"@example.com',
      '"a&b"@example.com',
      '"a%40b"@example.com',
      "a@b.c\nBcc: x",
    ]) {
      expect(supportMailto(bad), bad).toBeNull();
    }
  });
});
