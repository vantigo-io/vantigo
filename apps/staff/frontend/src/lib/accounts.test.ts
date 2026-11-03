import { describe, expect, it } from "vitest";
import { staffCatalog } from "../i18n";
import { accountKindName, accountKindsOffered, isProviderKind } from "./accounts";

const providers = [
  { key: "entra", name: "Microsoft Entra ID", status: "connected" as const },
  { key: "google", name: "Google Workspace", status: "unconfigured" as const },
];

const t = (key: string) => (staffCatalog.en as Record<string, string>)[key] ?? key;

describe("the account kinds (Google design G6)", () => {
  it("offers local, external and each provider's key, and keeps a kind already chosen", () => {
    expect(accountKindsOffered(providers)).toEqual(["local", "external", "entra", "google"]);
    expect(accountKindsOffered(undefined)).toEqual(["local", "external"]);
    expect(accountKindsOffered([], "okta")).toEqual(["local", "external", "okta"]);
  });

  it("names a provider's kind by the provider's name, else by the catalog, else by its key", () => {
    expect(accountKindName(t, "google", providers)).toBe("Google Workspace");
    expect(accountKindName(t, "local", providers)).toBe(staffCatalog.en.accountKindChoice_local);
    expect(accountKindName(t, "okta", providers)).toBe("okta");
  });

  it("tells a provider's kind from the two that need none", () => {
    expect(["local", "external", "entra", "google"].map(isProviderKind)).toEqual([false, false, true, true]);
  });
});
