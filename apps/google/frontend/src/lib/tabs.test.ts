import { describe, expect, it } from "vitest";
import { connected, key, unconfigured } from "../test/server";
import { opensOnConnection, stepsDone } from "./steps";
import { googleTabForPath, googleTabPath, googleTabs } from "./tabs";

describe("the Google tabs", () => {
  it("are four routes under /workspace/google", () => {
    expect(googleTabs.map((tab) => tab.path)).toEqual([
      "/workspace/google",
      "/workspace/google/connection",
      "/workspace/google/accounts",
      "/workspace/google/calendar-gmail",
    ]);
    expect(googleTabPath("calendar-gmail")).toBe("/workspace/google/calendar-gmail");
  });

  it("finds the tab a location belongs to, Status by default", () => {
    expect(googleTabForPath("/workspace/google")).toBe("status");
    expect(googleTabForPath("/workspace/google/connection")).toBe("connection");
    expect(googleTabForPath("/workspace/google/accounts/x")).toBe("accounts");
    expect(googleTabForPath("/workspace/google/calendar-gmail")).toBe("calendar-gmail");
    expect(googleTabForPath("/workspace/google/elsewhere")).toBe("status");
  });
});

describe("the steps", () => {
  it("opens on Connection only while unconfigured with no live key", () => {
    expect(opensOnConnection(unconfigured())).toBe(true);
    expect(opensOnConnection(unconfigured({ credentials: [key({ revokedAt: "2026-11-02T00:00:00Z" })] }))).toBe(true);
    expect(opensOnConnection(unconfigured({ credentials: [key()] }))).toBe(false);
    expect(opensOnConnection(connected({ status: "failing" }))).toBe(false);
  });

  it("marks each step done from the connection", () => {
    expect(stepsDone(unconfigured())).toEqual({ key: false, admin: false, tested: false, oauthClient: false });
    expect(stepsDone(connected())).toEqual({ key: true, admin: true, tested: true, oauthClient: true });
    expect(stepsDone(connected({ scopesMissing: ["x"] })).tested).toBe(false);
  });
});
