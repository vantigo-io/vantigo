import { describe, expect, it } from "vitest";
import {
  isOrgnrQuery,
  mapBrregEnhet,
  mapBrregSearchResponse,
  normalizeBrregPhone,
  normalizeOrgnr,
} from "./brreg";

describe("isOrgnrQuery", () => {
  it("detects 9-digit queries, ignoring whitespace", () => {
    expect(isOrgnrQuery("923609016")).toBe(true);
    expect(isOrgnrQuery("923 609 016")).toBe(true);
    expect(isOrgnrQuery("Equinor")).toBe(false);
    expect(isOrgnrQuery("12345678")).toBe(false);
    expect(isOrgnrQuery("1234567890")).toBe(false);
  });
});

describe("normalizeOrgnr", () => {
  it("strips whitespace", () => {
    expect(normalizeOrgnr("923 609 016")).toBe("923609016");
  });
});

describe("normalizeBrregPhone", () => {
  it("strips spaces and prefixes +47 for bare 8-digit numbers", () => {
    expect(normalizeBrregPhone("51 99 00 00")).toBe("+4751990000");
    expect(normalizeBrregPhone("922 34 614")).toBe("+4792234614");
  });

  it("keeps already-prefixed numbers, minus whitespace", () => {
    expect(normalizeBrregPhone("+47 51 99 00 00")).toBe("+4751990000");
  });

  it("returns undefined for empty values", () => {
    expect(normalizeBrregPhone(null)).toBeUndefined();
    expect(normalizeBrregPhone(undefined)).toBeUndefined();
    expect(normalizeBrregPhone("")).toBeUndefined();
  });
});

describe("mapBrregEnhet", () => {
  it("maps a full enhet", () => {
    expect(
      mapBrregEnhet({
        organisasjonsnummer: "923609016",
        navn: "EQUINOR ASA",
        organisasjonsform: { kode: "ASA" },
        forretningsadresse: { poststed: "STAVANGER" },
        epostadresse: "post@equinor.com",
        telefon: "51 99 00 00",
      }),
    ).toEqual({
      orgnr: "923609016",
      name: "EQUINOR ASA",
      orgForm: "ASA",
      city: "STAVANGER",
      email: "post@equinor.com",
      phone: "+4751990000",
    });
  });

  it("falls back to mobil when telefon is missing", () => {
    const company = mapBrregEnhet({
      organisasjonsnummer: "923609016",
      navn: "EQUINOR ASA",
      telefon: null,
      mobil: "922 34 614",
    });
    expect(company?.phone).toBe("+4792234614");
  });

  it("prefers telefon over mobil", () => {
    const company = mapBrregEnhet({
      organisasjonsnummer: "923609016",
      navn: "EQUINOR ASA",
      telefon: "51 99 00 00",
      mobil: "922 34 614",
    });
    expect(company?.phone).toBe("+4751990000");
  });

  it("tolerates missing optional fields", () => {
    expect(mapBrregEnhet({ organisasjonsnummer: "923609016", navn: "EQUINOR ASA" })).toEqual({
      orgnr: "923609016",
      name: "EQUINOR ASA",
      orgForm: undefined,
      city: undefined,
      email: undefined,
      phone: undefined,
    });
  });

  it("returns null when required fields are missing", () => {
    expect(mapBrregEnhet({ navn: "No orgnr" })).toBeNull();
    expect(mapBrregEnhet({ organisasjonsnummer: "923609016" })).toBeNull();
  });
});

describe("mapBrregSearchResponse", () => {
  it("maps the embedded list and drops invalid entries", () => {
    const body = {
      _embedded: {
        enheter: [
          { organisasjonsnummer: "923609016", navn: "EQUINOR ASA" },
          { navn: "Missing orgnr" },
        ],
      },
    };
    const results = mapBrregSearchResponse(body);
    expect(results).toHaveLength(1);
    expect(results[0].orgnr).toBe("923609016");
  });

  it("returns empty for malformed bodies", () => {
    expect(mapBrregSearchResponse(null)).toEqual([]);
    expect(mapBrregSearchResponse({})).toEqual([]);
    expect(mapBrregSearchResponse({ _embedded: { enheter: "nope" } })).toEqual([]);
  });
});
