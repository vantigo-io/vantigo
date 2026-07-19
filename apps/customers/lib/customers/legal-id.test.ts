import { describe, expect, it } from "vitest";
import { validateFodselsnummer, validateLegalId, validateOrgnr } from "./legal-id";

describe("validateOrgnr", () => {
  it("accepts valid Norwegian organisation numbers", () => {
    // Real, public orgnrs (Equinor, Brønnøysundregistrene)
    expect(validateOrgnr("923609016")).toBe(true);
    expect(validateOrgnr("974760673")).toBe(true);
  });

  it("rejects invalid checksums", () => {
    expect(validateOrgnr("923609017")).toBe(false);
    expect(validateOrgnr("974760674")).toBe(false);
  });

  it("rejects wrong lengths and non-digits", () => {
    expect(validateOrgnr("12345678")).toBe(false);
    expect(validateOrgnr("1234567890")).toBe(false);
    expect(validateOrgnr("92360901a")).toBe(false);
    expect(validateOrgnr("")).toBe(false);
  });
});

describe("validateFodselsnummer", () => {
  it("accepts a valid synthetic fødselsnummer", () => {
    // Synthetic number with correct mod11 check digits
    expect(validateFodselsnummer("01010150074")).toBe(true);
  });

  it("rejects invalid check digits", () => {
    expect(validateFodselsnummer("01010150075")).toBe(false);
    expect(validateFodselsnummer("01010150064")).toBe(false);
  });

  it("rejects wrong lengths and non-digits", () => {
    expect(validateFodselsnummer("0101015007")).toBe(false);
    expect(validateFodselsnummer("010101500741")).toBe(false);
    expect(validateFodselsnummer("0101015007a")).toBe(false);
  });
});

describe("validateLegalId", () => {
  it("applies orgnr rules for Norwegian businesses", () => {
    expect(validateLegalId("NO", "business", "923609016")).toBe(true);
    expect(validateLegalId("NO", "business", "923609017")).toBe(false);
    expect(validateLegalId("no", "business", "923609016")).toBe(true);
  });

  it("applies fødselsnummer rules for Norwegian private customers", () => {
    expect(validateLegalId("NO", "private", "01010150074")).toBe(true);
    expect(validateLegalId("NO", "private", "923609016")).toBe(false);
  });

  it("falls back to a basic sanity check for other countries", () => {
    expect(validateLegalId("SE", "business", "5560360793")).toBe(true);
    expect(validateLegalId("DE", "private", "abc")).toBe(false);
    expect(validateLegalId("DK", "business", "")).toBe(false);
  });
});
