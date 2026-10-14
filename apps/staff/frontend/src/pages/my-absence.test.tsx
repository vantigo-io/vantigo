import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { holidayAccount, myAbsences, selfCertification } from "../test/absence-fixtures";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

const typeDate = async (field: HTMLElement, text: string) => {
  await retype(field, text);
  await userEvent.tab();
};

/** The request sheet, opened from My page's Absence tab. */
const openSheet = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "Request absence" }));
  return screen.findByRole("dialog", { name: "Request absence" });
};

/** One of my absences, by its type and its days as the list writes them. */
const myAbsence = (name: string) => screen.findByRole("article", { name });

describe("My page — my holiday account", () => {
  it("shows the account the server answers, in working days", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=absence");

    const account = await region("Holiday account 2026");
    for (const [label, value] of [
      ["Entitlement", "25"],
      ["Carried in", "3"],
      ["Taken", "10"],
      ["Requested, waiting", "5"],
      ["Remaining", "18"],
    ])
      expect(within(account).getByText(label).nextElementSibling).toHaveTextContent(value);
    // Nothing was carried in automatically, so the line is not drawn.
    expect(within(account).queryByText("Carried in automatically")).not.toBeInTheDocument();
    expect(within(account).queryByText(/would take more holiday/)).not.toBeInTheDocument();
  });

  it("warns, without refusing, when what I asked for would overdraw it", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      absence: {
        holidayAccounts: [holidayAccount({ pending: 20, remaining: 18, overdrawn: true, carriedInDerived: 2 })],
      },
    });
    renderRoute("/staff/me?tab=absence");

    const account = await region("Holiday account 2026");
    expect(within(account).getByText(/would take more holiday than you have left/)).toBeInTheDocument();
    expect(within(account).getByText("Carried in automatically").nextElementSibling).toHaveTextContent("2");
  });
});

describe("My page — my absences", () => {
  it("lists my absences as I read them, my own HR-only request waiting by its name", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=absence");

    const pregnancy = await myAbsence("Pregnancy leave, Jan 4, 2027 – Mar 26, 2027");
    expect(within(pregnancy).getByText("Requested")).toBeInTheDocument();
    expect(within(pregnancy).getByText("60 working days")).toBeInTheDocument();
    const summer = await myAbsence("Holiday, Jul 6, 2026 – Jul 24, 2026");
    expect(within(summer).getByText("Approved")).toBeInTheDocument();
    expect(within(summer).getByText("Enjoy the summer")).toBeInTheDocument();
    // A decided or registered row is not mine to change or cancel.
    expect(within(summer).queryByRole("button")).not.toBeInTheDocument();
    const sick = await myAbsence("Self-certified sick leave, Sep 14, 2026 – Sep 15, 2026");
    expect(within(sick).getByText("Registered")).toBeInTheDocument();
    expect(within(sick).queryByRole("button")).not.toBeInTheDocument();
    const autumn = await myAbsence("Holiday, Nov 16, 2026 – Nov 20, 2026");
    expect(within(autumn).getByText("Autumn break")).toBeInTheDocument();
    expect(within(autumn).getByRole("button", { name: "Change" })).toBeInTheDocument();
    expect(within(autumn).getByRole("button", { name: "Cancel request" })).toBeInTheDocument();
  });

  it("never names a type the row does not carry", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      absence: {
        myAbsences: [{ id: 590, personId: 101, firstDay: "2026-10-19", lastDay: "2026-10-19", shownAs: "leave" }],
      },
    });
    renderRoute("/staff/me?tab=absence");

    const row = await myAbsence("Leave, Oct 19, 2026");
    // No status, no working days: the row carries neither.
    expect(within(row).queryByText(/Requested|Approved|Registered|working day/)).not.toBeInTheDocument();
  });

  it("cancels a request and keeps it as history", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=absence");

    const autumn = await myAbsence("Holiday, Nov 16, 2026 – Nov 20, 2026");
    await userEvent.click(within(autumn).getByRole("button", { name: "Cancel request" }));
    await userEvent.click(await screen.findByRole("button", { name: "Cancel the request" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/absences\/501\/cancel$/)).toHaveLength(1));
    expect(
      await within(await myAbsence("Holiday, Nov 16, 2026 – Nov 20, 2026")).findByText("Cancelled"),
    ).toBeInTheDocument();
  });

  it("changes a request with the revision it carries", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=absence");

    const autumn = await myAbsence("Holiday, Nov 16, 2026 – Nov 20, 2026");
    await userEvent.click(within(autumn).getByRole("button", { name: "Change" }));
    const dialog = await screen.findByRole("dialog", { name: "Change absence" });
    expect(within(dialog).getByRole("textbox", { name: "Note" })).toHaveValue("Autumn break");
    await typeDate(within(dialog).getByRole("textbox", { name: "Last day" }), "Nov 19, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/absences\/501$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/absences\/501$/);
    expect(put.ifMatch).toBe("2");
    expect(put.body).toEqual({ typeId: 1001, firstDay: "2026-11-16", lastDay: "2026-11-19", note: "Autumn break" });
  });

  it("sends the etag when that is what the row carries, quoted", async () => {
    const fetchMock = stubStaffApi({
      role: "access",
      me: 101,
      absence: {
        myAbsences: [{ ...myAbsences()[1], revision: undefined, etag: "a1-Zm9vYmFy" }],
      },
    });
    renderRoute("/staff/me?tab=absence");

    await userEvent.click(
      within(await myAbsence("Holiday, Nov 16, 2026 – Nov 20, 2026")).getByRole("button", { name: "Change" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Change absence" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/absences\/501$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/absences\/501$/)[0].ifMatch).toBe('"a1-Zm9vYmFy"');
  });
});

describe("My page — egenmelding and care days", () => {
  it("shows my standing under the statutory minimum by occurrences", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=absence");

    const standing = await region("Egenmelding");
    expect(within(standing).getByText("The statutory minimum")).toBeInTheDocument();
    expect(within(standing).getByText("Up to 3 calendar days")).toBeInTheDocument();
    expect(within(standing).getByText("1 of 4")).toBeInTheDocument();
    expect(within(standing).queryByText(/withdrawn/i)).not.toBeInTheDocument();
  });

  it("counts days under the extended policy, and names a withdrawal's start only when it is sent", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      absence: {
        selfCertification: selfCertification({
          policy: "extended",
          daysPerOccurrence: 8,
          occurrencesPerYear: undefined,
          daysPerYear: 24,
          daysUsed: 5,
          withdrawnUntil: "2027-01-31",
          withdrawnReason: "Agreed follow-up",
        }),
      },
    });
    renderRoute("/staff/me?tab=absence");

    const standing = await region("Egenmelding");
    expect(within(standing).getByText("The extended policy")).toBeInTheDocument();
    expect(within(standing).getByText("5 of 24")).toBeInTheDocument();
    expect(within(standing).queryByText("Occurrences in the last 12 months")).not.toBeInTheDocument();
    const withdrawn = within(standing).getByRole("alert");
    expect(withdrawn).toHaveTextContent("Your right to egenmelding is withdrawn until Jan 31, 2027: Agreed follow-up");
    expect(withdrawn).not.toHaveTextContent(/from/);
  });

  it("shows my care days, and no card when the server answers none", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=absence");

    const care = await region("Care days 2026");
    expect(within(care).getByText("Entitlement").nextElementSibling).toHaveTextContent("10");
    expect(within(care).getByText("Used").nextElementSibling).toHaveTextContent("3");
    expect(within(care).getByText("Remaining").nextElementSibling).toHaveTextContent("7");
  });

  it("leaves the care days out when they are not mine to read", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      read: (path) => (path.endsWith("/me/care-days") ? new Response(null, { status: 403 }) : undefined),
    });
    renderRoute("/staff/me?tab=absence");

    await region("Holiday account 2026");
    await region("Egenmelding");
    expect(screen.queryByRole("region", { name: /Care days/ })).not.toBeInTheDocument();
  });
});

describe("My page — the request sheet", () => {
  it("estimates the working days over the calendar's public holidays and the account after it", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101, absence: { publicHolidays: ["2026-10-14"] } });
    renderRoute("/staff/me?tab=absence");

    const dialog = await openSheet();
    await choose(dialog, "Type", "Holiday");
    await typeDate(within(dialog).getByRole("textbox", { name: "First day" }), "Oct 12, 2026");
    await typeDate(within(dialog).getByRole("textbox", { name: "Last day" }), "Oct 16, 2026");

    // Monday to Friday, the stubbed holiday on Wednesday skipped.
    expect(await within(dialog).findByText("About 4 working days")).toBeInTheDocument();
    // 18 remaining, 5 already asked for, 4 more.
    expect(within(dialog).getByText("About 9 days of holiday left after it")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => /\/calendar\?from=2026-10-12&to=2026-10-16$/.test(String(url)))).toBe(
      true,
    );

    await userEvent.click(within(dialog).getByRole("button", { name: "Send request" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/me\/absences$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/me\/absences$/)[0].body).toEqual({
      typeId: 1001,
      firstDay: "2026-10-12",
      lastDay: "2026-10-16",
    });
    // The server's answer, not the estimate.
    expect(await screen.findByText("Your request is sent")).toBeInTheDocument();
    expect(screen.getByText(/4 working days\. Holiday left in 2026: 18, of which 9 asked for\./)).toBeInTheDocument();
  });

  it("takes one day and hours for an hour type", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=absence");

    const dialog = await openSheet();
    await choose(dialog, "Type", "Doctor or dentist");
    expect(within(dialog).queryByRole("textbox", { name: "Last day" })).not.toBeInTheDocument();
    await typeDate(within(dialog).getByRole("textbox", { name: "Day" }), "Oct 13, 2026");
    await retype(within(dialog).getByRole("textbox", { name: "Hours" }), "1.5");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/me\/absences$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/me\/absences$/)[0].body).toEqual({
      typeId: 1020,
      firstDay: "2026-10-13",
      hours: 1.5,
    });
  });

  it("takes a percentage on a type that allows one, and a degree on a sykmelding without a note", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=absence");

    let dialog = await openSheet();
    await choose(dialog, "Type", "Parental leave");
    await typeDate(within(dialog).getByRole("textbox", { name: "First day" }), "Nov 2, 2026");
    await typeDate(within(dialog).getByRole("textbox", { name: "Last day" }), "Nov 27, 2026");
    await retype(within(dialog).getByRole("textbox", { name: "Percentage of each day" }), "50");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send request" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/me\/absences$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/me\/absences$/)[0].body).toEqual({
      typeId: 1007,
      firstDay: "2026-11-02",
      lastDay: "2026-11-27",
      percent: 50,
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    dialog = await openSheet();
    await choose(dialog, "Type", "Certified sick leave");
    expect(within(dialog).queryByRole("textbox", { name: "Note" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: "Percentage of each day" })).not.toBeInTheDocument();
    await typeDate(within(dialog).getByRole("textbox", { name: "First day" }), "Oct 12, 2026");
    await typeDate(within(dialog).getByRole("textbox", { name: "Last day" }), "Oct 23, 2026");
    await retype(within(dialog).getByRole("textbox", { name: "Degree" }), "60");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/me\/absences$/)).toHaveLength(2));
    expect(sent(fetchMock, "POST", /\/me\/absences$/)[1].body).toEqual({
      typeId: 1003,
      firstDay: "2026-10-12",
      lastDay: "2026-10-23",
      degreePercent: 60,
    });
  });

  it("shows what the policy allows before an egenmelding is sent, and its refusal in words on the type", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      write: (method, path) =>
        method === "POST" && path === "/api/v1/staff/me/absences"
          ? staffProblem(409, {
              code: "self_certification_exhausted",
              title: "Self-certification exhausted",
              detail: "The server's English.",
            })
          : undefined,
    });
    renderRoute("/staff/me?tab=absence");

    const dialog = await openSheet();
    await choose(dialog, "Type", "Self-certified sick leave");
    const allows = within(dialog).getByRole("status");
    expect(allows).toHaveTextContent("Up to 3 calendar days at a time");
    expect(allows).toHaveTextContent("You have used 1 of 4 in the last 12 months");
    expect(within(dialog).queryByRole("textbox", { name: "Note" })).not.toBeInTheDocument();
    await typeDate(within(dialog).getByRole("textbox", { name: "First day" }), "Oct 12, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register" }));

    expect(
      await within(dialog).findByText(
        "You have used the egenmelding the policy allows. Register a sykmelding from your doctor instead.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox", { name: "Type" })).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("puts an overlap on the first day, in words", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      write: (method, path) =>
        method === "POST" && path === "/api/v1/staff/me/absences"
          ? staffProblem(409, { code: "absence_overlap", title: "Absence overlap", detail: "The server's English." })
          : undefined,
    });
    renderRoute("/staff/me?tab=absence");

    const dialog = await openSheet();
    await choose(dialog, "Type", "Holiday");
    await typeDate(within(dialog).getByRole("textbox", { name: "First day" }), "Nov 16, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send request" }));

    expect(await within(dialog).findByText("You already have an absence on one of these days.")).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "First day" })).toHaveAttribute("aria-invalid", "true");
  });
});

describe("My page — the request sheet on a phone", () => {
  const phone = (query: string) => {
    const width = /max-width:\s*([\d.]+)em/.exec(query);
    return width !== null && Number(width[1]) >= 23.4375;
  };
  const original = window.matchMedia;

  afterEach(() => {
    window.matchMedia = original;
    window.innerWidth = 1024;
  });

  it("is a bottom sheet whose fields follow the type", async () => {
    window.innerWidth = 375;
    window.matchMedia = vi.fn((query: string) => ({
      matches: phone(query),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=absence");

    const dialog = await openSheet();
    expect(dialog.closest(".mantine-Drawer-content") ?? dialog.querySelector(".mantine-Drawer-content")).not.toBeNull();
    // What the sheet asks for, top to bottom, per kind of type — pinned so a
    // change to the fields or their words is a reviewed one.
    const outline = () =>
      [...dialog.querySelectorAll("label, [role=status], button[type=submit]")]
        .map((element) => element.textContent?.trim())
        .filter(Boolean);
    const shapes: Record<string, unknown> = {};
    for (const type of ["Holiday", "Doctor or dentist", "Parental leave", "Certified sick leave"]) {
      await choose(dialog, "Type", type);
      shapes[type] = outline();
    }
    expect(shapes).toMatchSnapshot();
  });
});
