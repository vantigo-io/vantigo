import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { EntraDirectoryMatches } from "../api/directory";
import { problemResponse } from "../test/api";
import { renderWithProviders } from "../test/render";
import { directoryUser, type EntraServer, stubEntraApi } from "../test/server";
import { DirectoryMatches, type DirectoryMatchesProps } from "./directory-matches";

const users = [
  { id: "u-1", displayName: "Kari Nordmann", email: "kari@example.com" },
  { id: "u-2", displayName: "Ola Hansen", email: "ola@example.com" },
  { id: "u-3", displayName: "Per Olsen", email: "per@example.com" },
];

const proposals: EntraDirectoryMatches = {
  matches: [
    { userId: "u-1", account: directoryUser(), matchedBy: ["mail", "userPrincipalName"] },
    {
      userId: "u-2",
      account: directoryUser({
        objectId: "55555555-5555-4555-8555-555555555555",
        displayName: "Ola Hansen",
        mail: "ola@contoso.no",
        userPrincipalName: "ola@contoso.no",
        employeeId: "1042",
      }),
      matchedBy: ["employeeId"],
    },
    {
      userId: "u-3",
      account: directoryUser({
        objectId: "66666666-6666-4666-8666-666666666666",
        displayName: "Per Olsen",
        mail: "per@contoso.no",
        userPrincipalName: "per@contoso.no",
        accountEnabled: false,
      }),
      matchedBy: ["mail"],
    },
  ],
  candidates: 5,
  ambiguous: 1,
  truncated: false,
};

const openReview = (server: EntraServer, props: Partial<DirectoryMatchesProps> = {}) => {
  stubEntraApi(server);
  const connect = vi.fn<DirectoryMatchesProps["connect"]>().mockResolvedValue(undefined);
  const onDone = vi.fn();
  renderWithProviders(<DirectoryMatches users={users} connect={connect} onDone={onDone} {...props} />);
  return { connect, onDone };
};

const rowOf = (name: string) => screen.getAllByText(name)[0].closest("tr") as HTMLElement;

describe("the directory's matches", () => {
  it("lists each proposal with what matched, checked only for an enabled account matched by principal name", async () => {
    openReview({ matches: proposals });
    expect(
      await screen.findByText(
        "Users not yet connected: 5. Matched exactly: 3. Left out because more than one account matched: 1.",
      ),
    ).toBeInTheDocument();
    expect(within(rowOf("kari@example.com")).getByText("Mail")).toBeInTheDocument();
    expect(within(rowOf("kari@example.com")).getByText("Principal name")).toBeInTheDocument();
    expect(within(rowOf("ola@example.com")).getByText("Employee ID")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Connect Kari Nordmann to kari@contoso.no" })).toBeChecked();
    // An employee ID alone, or a mail alone, is a weaker key: left to the Owner to check.
    expect(screen.getByRole("checkbox", { name: "Connect Ola Hansen to ola@contoso.no" })).not.toBeChecked();
    expect(within(rowOf("ola@example.com")).getByText("Check this match")).toBeInTheDocument();
    expect(within(rowOf("ola@example.com")).getByText(/Matched by mail or employee ID only/)).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Connect Per Olsen to per@contoso.no" })).not.toBeChecked();
    expect(within(rowOf("per@example.com")).getByText("Disabled")).toBeInTheDocument();
    expect(within(rowOf("kari@example.com")).queryByText("Check this match")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Connect selected (1)" })).toBeEnabled();
  });

  it("leaves an Owner's match unchecked, even by principal name, saying why", async () => {
    openReview({ matches: proposals }, { users: [{ ...users[0], owner: true }, users[1], users[2]] });
    const kari = await screen.findByRole("checkbox", { name: "Connect Kari Nordmann to kari@contoso.no" });
    expect(kari).not.toBeChecked();
    expect(within(rowOf("kari@example.com")).getByText("Check this match")).toBeInTheDocument();
    expect(within(rowOf("kari@example.com")).getByText(/This user is an Owner/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Connect selected (0)" })).toBeDisabled();
  });

  it("connects the chosen pairs one at a time and marks each row", async () => {
    const connect = vi.fn<DirectoryMatchesProps["connect"]>(async (userId) => {
      if (userId === "u-2") throw new Error("Another user is already connected to this account.");
    });
    const { onDone } = openReview({ matches: proposals }, { connect });
    await userEvent.click(await screen.findByRole("checkbox", { name: "Connect Ola Hansen to ola@contoso.no" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Connect Per Olsen to per@contoso.no" }));
    await userEvent.click(screen.getByRole("button", { name: "Connect selected (3)" }));
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
    expect(connect.mock.calls).toEqual([
      ["u-1", { objectId: proposals.matches[0].account.objectId, userPrincipalName: "kari@contoso.no" }],
      ["u-2", { objectId: proposals.matches[1].account.objectId, userPrincipalName: "ola@contoso.no" }],
      ["u-3", { objectId: proposals.matches[2].account.objectId, userPrincipalName: "per@contoso.no" }],
    ]);
    expect(within(rowOf("kari@example.com")).getByText("Connected")).toBeInTheDocument();
    expect(
      within(rowOf("ola@example.com")).getByText("Another user is already connected to this account."),
    ).toBeInTheDocument();
    expect(within(rowOf("per@example.com")).getByText("Connected")).toBeInTheDocument();
    // What failed stays chosen for another try; what connected is done.
    expect(screen.getByRole("button", { name: "Connect selected (1)" })).toBeEnabled();
  });

  it("connects nothing it was not asked to", async () => {
    const { connect } = openReview({ matches: proposals });
    await userEvent.click(await screen.findByRole("checkbox", { name: "Connect Ola Hansen to ola@contoso.no" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Connect Kari Nordmann to kari@contoso.no" }));
    await userEvent.click(screen.getByRole("button", { name: "Connect selected (1)" }));
    await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
    expect(connect.mock.calls[0][0]).toBe("u-2");
  });

  it("says so when nothing matches exactly, and when the list was cut", async () => {
    openReview({ matches: { matches: [], candidates: 1200, ambiguous: 0, truncated: true } });
    expect(await screen.findByText(/No user not yet connected matches exactly one account/)).toBeInTheDocument();
    expect(screen.getByText(/Only the first 1,000 users were compared/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Connect selected/ })).not.toBeInTheDocument();
  });

  it("says single sign-on is off in words", async () => {
    openReview({ matches: problemResponse(409, "refused", { code: "sso_not_configured" }) });
    expect(
      await screen.findByText("Single sign-on is not configured, so there is nothing to connect users to."),
    ).toBeInTheDocument();
  });

  it("says a throttled tenant in words", async () => {
    openReview({ matches: problemResponse(503, "refused", { code: "throttled" }) });
    expect(
      await screen.findByText("The tenant is busy and asked Vantigo to wait. Try again in a minute."),
    ).toBeInTheDocument();
  });
});
