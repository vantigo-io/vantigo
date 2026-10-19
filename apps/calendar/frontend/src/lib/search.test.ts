import { describe, expect, it } from "vitest";
import { validateCalendarSearch } from "./search";

const KARI = "22222222-2222-4222-8222-222222222222";

describe("validateCalendarSearch", () => {
  it("keeps what a link may carry", () => {
    expect(
      validateCalendarSearch({
        view: "week",
        date: "2026-10-21",
        user: KARI,
        appointment: 1001,
        occurrence: "2026-10-27",
        new: 1,
        customerId: 42,
        projectId: "7",
      }),
    ).toEqual({
      view: "week",
      date: "2026-10-21",
      user: KARI,
      appointment: 1001,
      occurrence: "2026-10-27",
      new: 1,
      customerId: 42,
      projectId: 7,
    });
  });

  it("leaves the view to the page when the URL names none", () => {
    expect(validateCalendarSearch({}).view).toBeUndefined();
  });

  it("drops what a hand-edited link invents", () => {
    expect(
      validateCalendarSearch({
        view: "year",
        date: "2026-13-45",
        user: "kari",
        appointment: "abc",
        occurrence: "2026-10-27",
        new: "yes",
        customerId: -1,
        projectId: 1.5,
      }),
    ).toEqual({
      view: undefined,
      date: undefined,
      user: undefined,
      appointment: undefined,
      occurrence: undefined,
      new: undefined,
      customerId: undefined,
      projectId: undefined,
    });
  });
});
