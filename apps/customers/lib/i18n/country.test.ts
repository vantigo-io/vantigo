import { describe, expect, it } from "vitest";
import { countryFlag, countryLabel, countryName } from "./country";

describe("countryFlag", () => {
  it("maps ISO codes to emoji flags", () => {
    expect(countryFlag("NO")).toBe("🇳🇴");
    expect(countryFlag("SE")).toBe("🇸🇪");
  });

  it("tolerates lowercase and whitespace", () => {
    expect(countryFlag("no")).toBe("🇳🇴");
    expect(countryFlag(" no ")).toBe("🇳🇴");
  });

  it("returns empty string for invalid input", () => {
    expect(countryFlag("NOR")).toBe("");
    expect(countryFlag("1")).toBe("");
    expect(countryFlag("")).toBe("");
  });
});

describe("countryName", () => {
  it("localizes country names", () => {
    expect(countryName("NO", "en")).toBe("Norway");
    expect(countryName("NO", "nb")).toBe("Norge");
    expect(countryName("DE", "en")).toBe("Germany");
  });

  it("falls back to the raw value for malformed input", () => {
    expect(countryName("invalid", "en")).toBe("invalid");
    expect(countryName("", "en")).toBe("");
  });
});

describe("countryLabel", () => {
  it("prefixes the flag", () => {
    expect(countryLabel("NO", "en")).toBe("🇳🇴 Norway");
    expect(countryLabel("NO", "nb")).toBe("🇳🇴 Norge");
  });
});
