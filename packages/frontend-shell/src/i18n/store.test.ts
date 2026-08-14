import { afterEach, describe, expect, it, vi } from "vitest";
import { getLanguagePreference, setLanguagePreference } from "./store";

afterEach(() => {
  setLanguagePreference("auto");
});

describe("language preference store", () => {
  it("switches locale immediately for an explicit account preference", () => {
    expect(setLanguagePreference("nb")).toBe("nb");
    expect(getLanguagePreference()).toBe("nb");

    expect(setLanguagePreference("en")).toBe("en");
    expect(getLanguagePreference()).toBe("en");
  });

  it("keeps the document language synchronized with the active locale", () => {
    const documentElement = { lang: "en" };
    vi.stubGlobal("document", { documentElement });

    setLanguagePreference("nb");
    expect(documentElement.lang).toBe("nb");

    setLanguagePreference("en");
    expect(documentElement.lang).toBe("en");
  });
});
