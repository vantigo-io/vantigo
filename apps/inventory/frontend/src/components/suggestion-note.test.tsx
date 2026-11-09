import { MantineProvider } from "@mantine/core";
import { act, render, screen } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";
import { suggestionOfLine } from "../lib/low-stock";
import { packagings } from "../test/server";
import { SuggestionNote, type SuggestionNoteProps } from "./suggestion-note";
import "../i18n";

const note = (props: SuggestionNoteProps) => {
  render(
    <MantineProvider env="test">
      <SuggestionNote {...props} />
    </MantineProvider>,
  );
  return document.querySelector("[data-suggestion]")?.textContent;
};

const box = { kind: "case", quantity: 15, count: 2 };

describe("the rounding sentence (I57)", () => {
  afterEach(() => act(() => setLanguagePreference("en")));

  it("says what was needed and how it was rounded up to whole packagings, by the packaging's kind", () => {
    expect(note({ needed: 21, suggested: 30, packaging: box })).toBe("21 needed, rounded up to 2 cases of 15 = 30");
  });

  it("says when the rounding passes the maximum, and by how much", () => {
    expect(note({ needed: 21, suggested: 30, packaging: box, maximum: 40 })).toBe(
      "21 needed, rounded up to 2 cases of 15 = 30, 9 above the maximum of 40",
    );
  });

  it("says what is needed alone without a packaging, or when nothing was rounded", () => {
    expect(note({ needed: 21, suggested: 21, packaging: null, maximum: 40 })).toBe("21 needed");
    act(() => document.body.replaceChildren());
    expect(note({ needed: 30, suggested: 30, packaging: box, maximum: 40 })).toBe("30 needed");
  });

  it("names one packaging in the singular, and an unknown kind as a packaging", () => {
    expect(note({ needed: 7, suggested: 15, packaging: { kind: "case", quantity: 15, count: 1 } })).toBe(
      "7 needed, rounded up to 1 case of 15 = 15",
    );
    act(() => document.body.replaceChildren());
    expect(note({ needed: 7, suggested: 12, packaging: { kind: "crate", quantity: 6, count: 2 } })).toBe(
      "7 needed, rounded up to 2 packagings of 6 = 12",
    );
  });

  it("says the same in Norwegian", () => {
    act(() => setLanguagePreference("nb"));
    expect(note({ needed: 21, suggested: 30, packaging: box })).toBe("21 trengs, rundet opp til 2 kartonger à 15 = 30");
    act(() => document.body.replaceChildren());
    expect(note({ needed: 21, suggested: 30, packaging: box, maximum: 40 })).toBe(
      "21 trengs, rundet opp til 2 kartonger à 15 = 30, 9 over maksbeholdningen på 40",
    );
    act(() => document.body.replaceChildren());
    expect(note({ needed: 1.5, suggested: 1.5, packaging: null })).toBe("1,5 trengs");
    act(() => document.body.replaceChildren());
    expect(note({ needed: 400, suggested: 600, packaging: { kind: "pallet", quantity: 600, count: 1 } })).toBe(
      "400 trengs, rundet opp til 1 pall à 600 = 600",
    );
  });

  it("reads a drafted line's suggestion through the variant's packagings", () => {
    expect(suggestionOfLine({ needed: 21, packagingId: 72, count: 2 }, packagings())).toEqual({
      needed: 21,
      suggested: 30,
      packaging: { kind: "case", quantity: 15, count: 2 },
    });
    // No packaging, or one the variant no longer has: what was needed alone.
    expect(suggestionOfLine({ needed: 5, packagingId: null, count: null }, packagings())).toEqual({
      needed: 5,
      suggested: 5,
    });
    expect(suggestionOfLine({ needed: 5, packagingId: 99, count: 1 }, packagings())).toEqual({
      needed: 5,
      suggested: 5,
    });
    expect(screen.queryByText(/needed/)).toBeNull();
  });
});
