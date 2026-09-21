import { describe, expect, it } from "vitest";
import { countryDisplayName, countrySelectData } from "./country-display";

describe("countrySelectData", () => {
  it("carries every officially assigned ISO 3166-1 alpha-2 code, lower-case", () => {
    const data = countrySelectData("en");
    expect(data).toHaveLength(249);
    expect(data.every((option) => option.value === option.value.toLowerCase())).toBe(true);
    expect(data.map((option) => option.value)).toContain("no");
  });

  it("pins Norway, Sweden, Denmark and Finland first, in that order", () => {
    const data = countrySelectData("en");
    expect(data.slice(0, 4).map((option) => option.value)).toEqual(["no", "se", "dk", "fi"]);
  });

  it("names the rest alphabetically by their localized display name", () => {
    const rest = countrySelectData("en").slice(4);
    const labels = rest.map((option) => option.label);
    expect(labels).toEqual([...labels].sort((a, b) => a.localeCompare(b, "en-US")));
  });

  it("localizes names for Norwegian Bokmål", () => {
    const enLabel = countryDisplayName("de", "en");
    const nbLabel = countryDisplayName("de", "nb");
    expect(enLabel).toBe("Germany");
    expect(nbLabel).not.toBe(enLabel);
  });
});
