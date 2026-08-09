import { appConfig, appUrl, hasSupportContact, initAppConfig, runtimeBase } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";

// In test mode the Vite base is "/", so without an injected runtime config
// these behave as root-serving defaults. The backend injects
// window.__VANTIGO_APP__ into index.html at serve time (SpaIndexDocument);
// these tests cover both sources.
afterEach(() => {
  delete window.__VANTIGO_APP__;
});

describe("appConfig", () => {
  it("falls back to build-time defaults when nothing is injected", () => {
    const config = appConfig();
    expect(config.basePath).toBe("/");
    expect(config.title).toBe("Vantigo");
    expect(config.logoUrl).toBeUndefined();
    expect(hasSupportContact(config)).toBe(false);
  });

  it("uses the dev default title from initAppConfig when nothing is injected", () => {
    initAppConfig({ title: "Customers" });
    try {
      expect(appConfig().title).toBe("Customers");
    } finally {
      initAppConfig({ title: "" });
    }
  });

  it("lets the injected title win over the dev default", () => {
    initAppConfig({ title: "Customers" });
    window.__VANTIGO_APP__ = { basePath: "/customers/", title: "Acme CRM", support: {} };
    try {
      expect(appConfig().title).toBe("Acme CRM");
    } finally {
      initAppConfig({ title: "" });
    }
  });

  it("prefers the backend-injected runtime config", () => {
    window.__VANTIGO_APP__ = {
      basePath: "/crm/",
      title: "Acme CRM",
      logoUrl: "https://cdn.acme.test/logo.svg",
      support: { email: "help@acme.test", phone: "+47 123 45 678", url: "https://support.acme.test" },
    };

    const config = appConfig();
    expect(config.basePath).toBe("/crm/");
    expect(config.title).toBe("Acme CRM");
    expect(config.logoUrl).toBe("https://cdn.acme.test/logo.svg");
    expect(config.support).toEqual({
      email: "help@acme.test",
      phone: "+47 123 45 678",
      url: "https://support.acme.test",
    });
    expect(hasSupportContact(config)).toBe(true);
  });

  it("normalizes injected nulls to undefined", () => {
    window.__VANTIGO_APP__ = {
      basePath: "/customers/",
      title: "Customers",
      logoUrl: null,
      support: { email: null, phone: null, url: null },
    };

    const config = appConfig();
    expect(config.logoUrl).toBeUndefined();
    expect(hasSupportContact(config)).toBe(false);
  });

  it("treats a single configured support field as a support contact", () => {
    window.__VANTIGO_APP__ = {
      basePath: "/customers/",
      title: "Customers",
      support: { phone: "+47 123 45 678" },
    };

    expect(hasSupportContact()).toBe(true);
  });
});

describe("runtimeBase", () => {
  it("falls back to the build-time Vite base when nothing is injected", () => {
    expect(runtimeBase()).toBe("/");
  });

  it("prefers the backend-injected base path", () => {
    window.__VANTIGO_APP__ = { basePath: "/crm/", title: "x", support: {} };
    expect(runtimeBase()).toBe("/crm/");
  });

  it("ignores an injected empty base path and falls back", () => {
    window.__VANTIGO_APP__ = { basePath: "", title: "x", support: {} };
    expect(runtimeBase()).toBe("/");
  });
});

describe("appUrl", () => {
  const setBase = (basePath: string) => {
    window.__VANTIGO_APP__ = { basePath, title: "x", support: {} };
  };

  it("returns root-relative URLs unchanged when serving from the root", () => {
    expect(appUrl("/auth/session")).toBe("/auth/session");
    expect(appUrl("auth/session")).toBe("/auth/session");
  });

  it("prefixes root-relative URLs with the injected base path", () => {
    setBase("/customers/");
    expect(appUrl("/auth/session")).toBe("/customers/auth/session");
    expect(appUrl("/api/v1/customers")).toBe("/customers/api/v1/customers");
  });

  it("never produces a double slash between base and path", () => {
    setBase("/customers/");
    expect(appUrl("/sign-in")).toBe("/customers/sign-in");
  });

  it("handles a base path injected without a trailing slash", () => {
    setBase("/customers");
    expect(appUrl("/sign-in")).toBe("/customers/sign-in");
  });

  it("supports nested base paths", () => {
    setBase("/apps/customers/");
    expect(appUrl("/auth/antiforgery")).toBe("/apps/customers/auth/antiforgery");
  });
});
