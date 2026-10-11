import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

describe("My page — a person not linked yet", () => {
  it("explains that there is no record yet, without an error", async () => {
    stubStaffApi({ role: "access" });
    renderRoute("/staff/me");

    expect(await screen.findByText("You have no record in Staff yet")).toBeInTheDocument();
    expect(screen.getByText(/HR links it to the user you sign in as/)).toBeInTheDocument();
    expect(screen.queryByText(/No person|linked to no person/)).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("My page — my own record", () => {
  it("shows my details, the sealed fields as on file and no HR notes", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    expect(await screen.findByRole("heading", { name: "My page", level: 2 })).toBeInTheDocument();
    expect(within(await region("Work")).getByText("ada@fjord.example")).toBeInTheDocument();
    expect(within(await region("Private")).getByText("+47 912 34 567")).toBeInTheDocument();
    const identifiers = await region("Identifiers");
    expect(within(identifiers).getByText("On file")).toBeInTheDocument();
    expect(within(identifiers).getByText("Ending in 8903")).toBeInTheDocument();
    expect(within(identifiers).queryByRole("button")).not.toBeInTheDocument();
    expect(within(await region("Next of kin")).getByText("Byron Lovelace")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "HR's notes" })).not.toBeInTheDocument();
    expect(screen.getByText(/ask HR/)).toBeInTheDocument();
  });

  it("shows my employments with the pay in my terms", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=employments");

    const latest = await region("Terms from Mar 1, 2026");
    expect(within(latest).getByText(/Base salary/)).toBeInTheDocument();
    expect(within(latest).queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByText("Hired through the referral programme")).not.toBeInTheDocument();
  });

  it("leaves HR's note off my terms even when I am HR myself", async () => {
    stubStaffApi({ role: "manage", me: 101 });
    renderRoute("/staff/me?tab=employments");

    const first = await region("Terms from Jan 1, 2025 to Feb 28, 2026");
    expect(within(first).getByText("Engineer")).toBeInTheDocument();
    expect(screen.queryByText("Hired through the referral programme")).not.toBeInTheDocument();
  });

  it("opens the form on my record as it is now, so HR's newer change is neither lost nor overwritten", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    await region("Private");
    // HR saves meanwhile; the page has not read it yet.
    const ada = fetchMock.state.people.find((one) => one.id === 101);
    if (ada?.record) {
      ada.record.city = "Bergen";
      ada.record.revision = 4;
    }
    await userEvent.click(screen.getByRole("button", { name: "Edit my details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit my details" });
    expect(await within(dialog).findByRole("textbox", { name: "City" })).toHaveValue("Bergen");
    await retype(within(dialog).getByRole("textbox", { name: "Mobile phone" }), "+47 922 22 222");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/me$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/staff\/me$/);
    expect(put.ifMatch).toBe("4");
    expect(put.body).toMatchObject({ city: "Bergen", phoneMobile: "+47 922 22 222" });
  });

  it("shows my history without HR's notes", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=history");

    const list = await screen.findAllByRole("article");
    expect(list).toHaveLength(3);
    expect(screen.getByText("Work email: ada@old.example → ada@fjord.example")).toBeInTheDocument();
    expect(screen.queryByText(/standing desk/)).not.toBeInTheDocument();
  });

  it("saves my private details and next of kin with the revision it read, and nothing of the work group", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    await userEvent.click(await screen.findByRole("button", { name: "Edit my details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit my details" });
    expect(within(dialog).queryByRole("textbox", { name: "Work email" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: "Given name" })).not.toBeInTheDocument();
    await retype(within(dialog).getByRole("textbox", { name: "Mobile phone" }), "+47 911 11 111");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/me$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/staff\/me$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body).toEqual({
      phoneMobile: "+47 911 11 111",
      privateEmail: "ada@home.example",
      street: "Storgata 1",
      postalCode: "0155",
      city: "Oslo",
      country: "NO",
      nextOfKin: [{ name: "Byron Lovelace", relationship: "spouse", phone: "+47 400 00 000" }],
    });
    expect(await within(await region("Private")).findByText("+47 911 11 111")).toBeInTheDocument();
  });

  it("reloads with the reader's sentence when HR saved first", async () => {
    const fetchMock = stubStaffApi({
      role: "access",
      me: 101,
      write: (method, path) =>
        method === "PUT" && path === "/api/v1/staff/me"
          ? staffProblem(409, {
              code: "revision_conflict",
              title: "Revision conflict",
              detail: "The person has revision 4; the supplied revision was 3.",
            })
          : undefined,
    });
    renderRoute("/staff/me");

    await userEvent.click(await screen.findByRole("button", { name: "Edit my details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit my details" });
    const ada = fetchMock.state.people.find((one) => one.id === 101);
    if (ada?.record) ada.record.city = "Bergen";
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
    expect(screen.queryByText(/supplied revision/)).not.toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Edit my details" })).not.toBeInTheDocument());
    expect(await within(await region("Private")).findByText("Bergen")).toBeInTheDocument();
  });
});

describe("My page — on a phone", () => {
  // A phone is narrower than Mantine's `sm` (48em): every max-width query at
  // or above it matches, and nothing wider does.
  const phone = (query: string) => {
    const width = /max-width:\s*([\d.]+)em/.exec(query);
    return width !== null && Number(width[1]) >= 23.4375;
  };
  const original = window.matchMedia;

  afterEach(() => {
    window.matchMedia = original;
    window.innerWidth = 1024;
  });

  it("is one column, and its form is a full-screen sheet", async () => {
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
    renderRoute("/staff/me");

    // Every group in, so the outline is the whole page and not a moment of its loading.
    for (const name of ["Private", "Identifiers", "Next of kin", "Roles", "Self-service", "Organisation"])
      await region(name);
    await screen.findByText("Byron Lovelace");
    await screen.findByText("Safety representative (verneombud)");
    // What a phone reads, top to bottom: the groups one after the other, each
    // label with its value, the actions in reach — pinned so a change to the
    // order or the words is a reviewed one.
    const outline = [...document.querySelectorAll("h2, h3, [role=tab], section dt, section dd, section p, button")]
      .map(
        (element) =>
          `${element.tagName.toLowerCase()}${element.getAttribute("role") === "tab" ? "[tab]" : ""}: ${element.textContent?.trim()}`,
      )
      .filter((line) => !line.endsWith(": "));
    expect(outline).toMatchSnapshot();
    // Every field grid declares one column at the base width — the phone's —
    // and two only from Mantine's `sm` up.
    const grids = [...document.querySelectorAll("dl")];
    expect(grids.length).toBeGreaterThan(0);
    const css = [...document.querySelectorAll("style")].map((style) => style.textContent ?? "").join("\n");
    for (const grid of grids) {
      const scope = [...grid.classList].find((name) => css.includes(`.${name}`)) as string;
      expect(scope, "a grid without its own column rule").toBeDefined();
      const escaped = scope.replace(/[-_]/g, "\\$&");
      expect(css).toMatch(new RegExp(`\\.${escaped}\\{[^}]*--sg-cols:\\s*1;`));
      expect(css).toMatch(new RegExp(`@media\\(min-width: 48em\\)\\{\\.${escaped}\\{--sg-cols:\\s*2;`));
    }

    await userEvent.click(screen.getByRole("button", { name: "Edit my details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit my details" });
    expect(dialog.closest("[data-full-screen]") ?? dialog.querySelector("[data-full-screen]")).not.toBeNull();
  });
});
