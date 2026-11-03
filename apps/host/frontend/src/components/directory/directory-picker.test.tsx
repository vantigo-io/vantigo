import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import "../../i18n";
import {
  type DirectoryServer,
  directoryUser,
  identityRefusal,
  renderWithProviders,
  stubDirectoryApi,
} from "../../test/directory";
import { DirectoryPicker, type DirectoryPickerProps } from "./directory-picker";

const KARI = directoryUser();
const OLA = directoryUser({
  objectId: "55555555-5555-4555-8555-555555555555",
  displayName: "Ola Nordmann",
  mail: "ola@contoso.no",
  userPrincipalName: "ola@contoso.no",
  employeeNumber: "1042",
  enabled: false,
});

const openPicker = (server: DirectoryServer, props: Partial<DirectoryPickerProps> = {}) => {
  const fetchMock = stubDirectoryApi(server);
  const onPick = vi.fn();
  renderWithProviders(<DirectoryPicker onPick={onPick} {...props} />);
  return { fetchMock, onPick, field: screen.getByLabelText(/Search the directory/) };
};

const searches = (fetchMock: ReturnType<typeof stubDirectoryApi>) =>
  fetchMock.actualCalls
    .map(([input]) => String(input))
    .filter((url) => url.startsWith("/api/v1/identity/owner/sso/directory/users"));

describe("the directory picker", () => {
  it("asks nothing before two characters, then once after the pause", async () => {
    const { fetchMock, field } = openPicker({ users: [KARI, OLA] });
    await userEvent.type(field, "n");
    expect(await screen.findByText("Type at least two characters.")).toBeInTheDocument();
    await userEvent.type(field, "ordm");
    expect(await screen.findByRole("option", { name: /Kari Nordmann/ })).toBeInTheDocument();
    expect(searches(fetchMock)).toEqual(["/api/v1/identity/owner/sso/directory/users?search=nordm"]);
  });

  it("shows each account's principal name, employee number and state", async () => {
    const { field } = openPicker({ users: [KARI, OLA] });
    await userEvent.type(field, "nordmann");
    const ola = await screen.findByRole("option", { name: /Ola Nordmann/ });
    expect(ola).toHaveTextContent("ola@contoso.no · employee number 1042");
    expect(ola).toHaveTextContent("Disabled");
    expect(screen.getByRole("option", { name: /Kari Nordmann/ })).not.toHaveTextContent("Disabled");
  });

  it("answers the picked account's two keys", async () => {
    const { field, onPick } = openPicker({ users: [KARI] });
    await userEvent.type(field, "kari");
    await userEvent.click(await screen.findByRole("option", { name: /Kari Nordmann/ }));
    expect(onPick).toHaveBeenCalledWith({ objectId: KARI.objectId, userPrincipalName: "kari@contoso.no" });
    expect(field).toHaveValue("Kari Nordmann");
  });

  it("tells the host to forget the pick once the search changes after it, and only then", async () => {
    const onClear = vi.fn();
    const { field } = openPicker({ users: [KARI] }, { onClear });
    await userEvent.type(field, "kari");
    expect(onClear).not.toHaveBeenCalled();
    await userEvent.click(await screen.findByRole("option", { name: /Kari Nordmann/ }));
    await userEvent.type(field, "x");
    expect(onClear).toHaveBeenCalledTimes(1);
    await userEvent.type(field, "y");
    expect(onClear).toHaveBeenCalledTimes(1);
  });

  it("marks a guest account", async () => {
    const { field } = openPicker({ users: [KARI, { ...OLA, guest: true }] });
    await userEvent.type(field, "nordmann");
    expect(await screen.findByRole("option", { name: /Ola Nordmann/ })).toHaveTextContent("Guest");
    expect(screen.getByRole("option", { name: /Kari Nordmann/ })).not.toHaveTextContent("Guest");
  });

  it("marks a held account and keeps it from being picked, unless it is the user's own", async () => {
    const { field, onPick } = openPicker(
      {
        users: [
          directoryUser({ holder: { kind: "expected", userId: "u-2" } }),
          { ...OLA, holder: { kind: "expected", userId: "u-1" } },
          directoryUser({
            objectId: "66666666-6666-4666-8666-666666666666",
            displayName: "Nora Nordmann",
            userPrincipalName: "nora@contoso.no",
            holder: { kind: "account" },
          }),
          directoryUser({
            objectId: "77777777-7777-4777-8777-777777777777",
            displayName: "Per Nordmann",
            userPrincipalName: "per@contoso.no",
            holder: { kind: "mapped", userId: "u-3" },
          }),
        ],
      },
      { forUserId: "u-1", userName: (id) => (id === "u-2" ? "Kari Hansen" : undefined) },
    );
    await userEvent.type(field, "nordmann");
    const kari = await screen.findByRole("option", { name: /Kari Nordmann/ });
    expect(kari).toHaveTextContent("Awaiting Kari Hansen");
    expect(kari).toHaveAttribute("data-combobox-disabled");
    expect(screen.getByRole("option", { name: /Nora Nordmann/ })).toHaveTextContent("Announced by Staff");
    expect(screen.getByRole("option", { name: /Per Nordmann/ })).toHaveTextContent("Provisioned to another user");
    await userEvent.click(kari);
    expect(onPick).not.toHaveBeenCalled();
    // Ola's account is announced for the user being connected: it stays selectable.
    const ola = screen.getByRole("option", { name: /Ola Nordmann/ });
    expect(ola).toHaveTextContent("Awaiting another user");
    await userEvent.click(ola);
    expect(onPick).toHaveBeenCalledWith({ objectId: OLA.objectId, userPrincipalName: "ola@contoso.no" });
  });

  it("says to narrow the search when 25 accounts come back", async () => {
    const many = Array.from({ length: 30 }, (_, index) =>
      directoryUser({
        objectId: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
        displayName: `Person ${index}`,
        userPrincipalName: `person${index}@contoso.no`,
      }),
    );
    const { field } = openPicker({ users: many });
    await userEvent.type(field, "person");
    expect(await screen.findByText(/Narrow the search/)).toBeInTheDocument();
    expect(screen.getAllByRole("option")).toHaveLength(25);
  });

  it("says in words why the directory could not be read", async () => {
    const { field } = openPicker({ users: identityRefusal(503, "provider_unreachable") });
    await userEvent.type(field, "kari");
    expect(await screen.findByText(/The directory could not be asked/)).toBeInTheDocument();
  });

  it("says in words when there is no directory to search", async () => {
    const { field } = openPicker({ users: identityRefusal(409, "no_directory") });
    await userEvent.type(field, "kari");
    expect(await screen.findByText(/No directory serves the configured single sign-on provider/)).toBeInTheDocument();
  });

  it("says when nothing matches", async () => {
    const { field } = openPicker({ users: [KARI] });
    await userEvent.type(field, "zz");
    await waitFor(() => expect(screen.getByText("No account in the directory matches.")).toBeInTheDocument());
  });
});
