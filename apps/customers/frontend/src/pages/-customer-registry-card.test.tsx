import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerRegistryCard } from "./-customer-registry-card";
import { CustomerOverview } from "./customers.$customerId";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const problemResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/problem+json" } });

const identityBody = {
  country: "no",
  type: "business",
  id: "974760673",
  name: "REGISTERENHETEN I BRØNNØYSUND",
  source: "brreg",
};

/**
 * Literally the body the server sends (`registryRecordResponse` in
 * `apps/server/internal/customers/registry.go`): the optional fields the
 * registry holds nothing for — here `mobile` and `deletedOn` — are absent,
 * not null.
 */
const fullRecordBody = {
  organisationNumber: "974760673",
  name: "REGISTERENHETEN I BRØNNØYSUND",
  organisationFormCode: "ORGL",
  organisationForm: "Organisasjonsledd",
  industryCode: "84.110",
  industry: "Generell offentlig administrasjon",
  employees: 487,
  vatRegistered: true,
  bankrupt: false,
  underLiquidation: false,
  underForcedLiquidation: false,
  foundedOn: "1995-08-09",
  website: "www.brreg.no",
  email: "firmapost@brreg.no",
  phone: "75 00 75 09",
  parentOrganisationNumber: "912660680",
  businessAddress: {
    lines: ["Havnegata 48"],
    postalCode: "8900",
    city: "BRØNNØYSUND",
    municipality: "BRØNNØY",
    countryCode: "NO",
  },
  postalAddress: {
    lines: ["Postboks 900"],
    postalCode: "8910",
    city: "BRØNNØYSUND",
    municipality: "BRØNNØY",
    countryCode: "NO",
  },
  fetchedAt: "2026-09-22T09:00:00Z",
};

/** The seven fields the contract requires, and nothing else. */
const minimalRecordBody = {
  organisationNumber: "974760673",
  name: "REGISTERENHETEN I BRØNNØYSUND",
  vatRegistered: false,
  bankrupt: false,
  underLiquidation: false,
  underForcedLiquidation: false,
  fetchedAt: "2026-09-22T09:00:00Z",
};

/** Answers the two GETs the card makes, and nothing else — a POST is a test's own to add. */
const registryFetch = (record: unknown, extra?: (url: string, init?: RequestInit) => unknown) =>
  vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url);
    const fromExtra = extra?.(path, init);
    if (fromExtra) return fromExtra;
    if (path === "/api/v1/customers/1001/legal-identity") return Promise.resolve(jsonResponse(200, identityBody));
    if (path === "/api/v1/customers/1001/registry-record") {
      return Promise.resolve(record === null ? new Response(null, { status: 204 }) : jsonResponse(200, record));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderCard = (
  fetchMock: ReturnType<typeof vi.fn>,
  { canManageIdentity = true }: { canManageIdentity?: boolean } = {},
) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <CustomerRegistryCard customerId={1001} canManageIdentity={canManageIdentity} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return queryClient;
};

/** Every GET of the record the card has made — never "the last fetch". */
const recordGets = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls.filter(
    ([url, init]) =>
      String(url) === "/api/v1/customers/1001/registry-record" &&
      (init as RequestInit | undefined)?.method === undefined,
  );

const refreshPosts = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls.filter(
    ([url, init]) =>
      String(url) === "/api/v1/customers/1001/registry-refresh" && (init as RequestInit | undefined)?.method === "POST",
  );

const lastIdentityPut = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls
    .filter(
      ([url, init]) =>
        String(url) === "/api/v1/customers/1001/legal-identity" && (init as RequestInit | undefined)?.method === "PUT",
    )
    .at(-1);

describe("CustomerRegistryCard", () => {
  it("titles the card with a heading, as its siblings on the tab do", async () => {
    renderCard(registryFetch(fullRecordBody));
    expect(await screen.findByRole("heading", { name: "Registry", level: 3 })).toBeInTheDocument();
  });

  it("renders every field the registry holds, each under its own label", async () => {
    renderCard(registryFetch(fullRecordBody));

    expect(await screen.findByText("Organisasjonsledd")).toBeInTheDocument();
    expect(screen.getByText("84.110 — Generell offentlig administrasjon")).toBeInTheDocument();
    expect(screen.getByText("487")).toBeInTheDocument();
    expect(screen.getByText("Yes")).toBeInTheDocument();
    // Founded on, formatted for the locale rather than shown as an ISO date.
    expect(screen.getByText("Founded")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "www.brreg.no" })).toHaveAttribute("href", "https://www.brreg.no");
    expect(screen.getByRole("link", { name: "firmapost@brreg.no" })).toHaveAttribute(
      "href",
      "mailto:firmapost@brreg.no",
    );
    expect(screen.getByRole("link", { name: "75 00 75 09" })).toHaveAttribute("href", "tel:75007509");
    // Both of the registry's addresses, line by line.
    expect(screen.getByText("Havnegata 48")).toBeInTheDocument();
    expect(screen.getByText("8900 BRØNNØYSUND")).toBeInTheDocument();
    expect(screen.getByText("Postboks 900")).toBeInTheDocument();
    expect(screen.getByText("8910 BRØNNØYSUND")).toBeInTheDocument();
  });

  it("shows the parent organisation number as text, not as a link to a customer that may not exist", async () => {
    renderCard(registryFetch(fullRecordBody));

    expect(await screen.findByText("912660680")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "912660680" })).not.toBeInTheDocument();
  });

  it("says where the record comes from and when it was read", async () => {
    renderCard(registryFetch(fullRecordBody));
    expect(await screen.findByText(/^From Brønnøysundregistrene, fetched /)).toBeInTheDocument();
  });

  it("says the register reported a change the record does not have yet", async () => {
    renderCard(registryFetch({ ...fullRecordBody, registryUpdatedHint: "2026-09-23T08:00:00Z" }));
    expect(await screen.findByText(/^The registry reported a change on /)).toBeInTheDocument();
  });

  it("says nothing when the record is at least as new as the register's report", async () => {
    // fetchedAt is 2026-09-22T09:00:00Z: a refresh has already caught up.
    renderCard(registryFetch({ ...fullRecordBody, registryUpdatedHint: "2026-09-22T08:00:00Z" }));
    expect(await screen.findByText(/^From Brønnøysundregistrene, fetched /)).toBeInTheDocument();
    expect(screen.queryByText(/^The registry reported a change on /)).not.toBeInTheDocument();
  });

  it("says nothing when the register has reported nothing", async () => {
    renderCard(registryFetch(fullRecordBody));
    expect(await screen.findByText(/^From Brønnøysundregistrene, fetched /)).toBeInTheDocument();
    expect(screen.queryByText(/^The registry reported a change on /)).not.toBeInTheDocument();
  });

  it("renders a foreign address the register sent no country code for, exactly as before", async () => {
    renderCard(
      registryFetch({
        ...minimalRecordBody,
        businessAddress: { lines: ["ul. Budowniczych 12"], city: "81-336 GDYNIA" },
      }),
    );
    expect(await screen.findByText("ul. Budowniczych 12")).toBeInTheDocument();
    expect(screen.getByText("81-336 GDYNIA")).toBeInTheDocument();
  });

  it("invents nothing for a record that carries only the required fields", async () => {
    renderCard(registryFetch(minimalRecordBody));

    // The one required fact that is not the identity itself.
    expect(await screen.findByText("No")).toBeInTheDocument();
    expect(screen.queryByText("Industry")).not.toBeInTheDocument();
    expect(screen.queryByText("Employees")).not.toBeInTheDocument();
    expect(screen.queryByText("Business address")).not.toBeInTheDocument();
    expect(screen.queryByText("Parent organisation")).not.toBeInTheDocument();
  });

  it("badges a bankrupt entity, one under liquidation, and one struck from the register", async () => {
    renderCard(
      registryFetch({
        ...fullRecordBody,
        bankrupt: true,
        underLiquidation: true,
        underForcedLiquidation: true,
        deletedOn: "2026-08-01",
      }),
    );

    expect(await screen.findByText("Bankrupt")).toBeInTheDocument();
    expect(screen.getByText("Under liquidation")).toBeInTheDocument();
    expect(screen.getByText("Under compulsory liquidation")).toBeInTheDocument();
    expect(screen.getByText(/^Deleted /)).toBeInTheDocument();
  });

  it("says the record has not been fetched yet when the server answers 204", async () => {
    renderCard(registryFetch(null));

    expect(await screen.findByText("Registry record not fetched yet")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument();
    expect(screen.queryByText(/^From Brønnøysundregistrene/)).not.toBeInTheDocument();
  });

  it("offers no Refresh without canManageIdentity", async () => {
    renderCard(registryFetch(null), { canManageIdentity: false });

    expect(await screen.findByText("Registry record not fetched yet")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Refresh" })).not.toBeInTheDocument();
  });

  // Task 2 review: mounted together with Refresh itself, not only once there
  // is something to announce, so a screen reader already has the region and a
  // click's own outcome is announced rather than merely drawn.
  it("mounts the status region as soon as Refresh is offered, before any click", async () => {
    renderCard(registryFetch(null));

    expect(await screen.findByRole("button", { name: "Refresh" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toBeInTheDocument();
  });

  it("mounts no status region for a caller Refresh is withheld from, with nothing yet to announce", async () => {
    renderCard(registryFetch(null), { canManageIdentity: false });

    await screen.findByText("Registry record not fetched yet");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("says the record could not be loaded rather than claiming there is none", async () => {
    const fetchMock = registryFetch(null, (path) =>
      path === "/api/v1/customers/1001/registry-record" ? Promise.resolve(new Response(null, { status: 500 })) : null,
    );
    renderCard(fetchMock);

    expect(await screen.findByText("Could not load the registry record.")).toBeInTheDocument();
    expect(screen.queryByText("Registry record not fetched yet")).not.toBeInTheDocument();
  });

  it("Refresh posts, stays pending while the registry answers, and reads the record again", async () => {
    let answer: (value: Response) => void = () => undefined;
    const pending = new Promise<Response>((resolve) => {
      answer = resolve;
    });
    const fetchMock = registryFetch(minimalRecordBody, (path, init) =>
      path === "/api/v1/customers/1001/registry-refresh" && init?.method === "POST" ? pending : null,
    );
    renderCard(fetchMock);

    const button = await screen.findByRole("button", { name: "Refresh" });
    const getsBefore = recordGets(fetchMock).length;
    await userEvent.click(button);

    await waitFor(() => expect(refreshPosts(fetchMock)).toHaveLength(1));
    expect(button).toBeDisabled();

    answer(jsonResponse(200, { status: "found", record: fullRecordBody, changes: [] }));

    await waitFor(() => expect(recordGets(fetchMock).length).toBeGreaterThan(getsBefore));
    expect(button).not.toBeDisabled();
  });

  it("reports what changed once, with the timeline the place to read it — and dismisses it on request", async () => {
    const fetchMock = registryFetch(fullRecordBody, (path, init) =>
      path === "/api/v1/customers/1001/registry-refresh" && init?.method === "POST"
        ? Promise.resolve(
            jsonResponse(200, {
              status: "found",
              record: fullRecordBody,
              changes: [
                { field: "employees", from: "480", to: "487" },
                { field: "email", to: "firmapost@brreg.no" },
              ],
            }),
          )
        : null,
    );
    const queryClient = renderCard(fetchMock);
    queryClient.setQueryData(["customers", 1001, "timeline"], { data: [], nextCursor: null });
    queryClient.setQueryData(["customers", 1001], { id: 1001, revision: 4 });

    await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));

    expect(await screen.findByText("2 changes — see the timeline")).toBeInTheDocument();
    // The events the refresh wrote are on this same page; the customer row
    // itself never moves for a registry read, so its query is left alone.
    expect(queryClient.getQueryState(["customers", 1001, "timeline"])?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(["customers", 1001])?.isInvalidated).toBe(false);

    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText("2 changes — see the timeline")).not.toBeInTheDocument();
  });

  // Task 10 review: each click answers for itself (`onMutate` above), so a
  // second, empty-handed Refresh must clear what the first one left behind
  // rather than leaving a stale "N changes" line under a fresh reading.
  it("a second Refresh clears the previous 'N changes' line", async () => {
    let changes: { field: string; from?: string; to?: string }[] = [{ field: "employees", from: "480", to: "487" }];
    const fetchMock = registryFetch(fullRecordBody, (path, init) =>
      path === "/api/v1/customers/1001/registry-refresh" && init?.method === "POST"
        ? Promise.resolve(jsonResponse(200, { status: "found", record: fullRecordBody, changes }))
        : null,
    );
    const queryClient = renderCard(fetchMock);
    queryClient.setQueryData(["customers", 1001, "timeline"], { data: [], nextCursor: null });

    await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));
    expect(await screen.findByText("1 change — see the timeline")).toBeInTheDocument();

    changes = [];
    await userEvent.click(screen.getByRole("button", { name: "Refresh" }));

    await waitFor(() => expect(screen.queryByText(/see the timeline/)).not.toBeInTheDocument());
  });

  it("says nothing about changes when a refresh found none", async () => {
    const fetchMock = registryFetch(fullRecordBody, (path, init) =>
      path === "/api/v1/customers/1001/registry-refresh" && init?.method === "POST"
        ? Promise.resolve(jsonResponse(200, { status: "found", record: fullRecordBody, changes: [] }))
        : null,
    );
    const queryClient = renderCard(fetchMock);
    queryClient.setQueryData(["customers", 1001, "timeline"], { data: [], nextCursor: null });

    const getsBefore = recordGets(fetchMock).length;
    await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));

    // Waits for the record's own refetch (task 9 review), not just the POST:
    // the absence this asserts is only settled once the card has read back
    // what the refresh actually stored, the same thing every other assertion
    // in this file about "what's on screen after a refresh" waits for.
    await waitFor(() => expect(recordGets(fetchMock).length).toBeGreaterThan(getsBefore));
    expect(screen.queryByText(/see the timeline/)).not.toBeInTheDocument();
    expect(queryClient.getQueryState(["customers", 1001, "timeline"])?.isInvalidated).toBe(false);
  });

  it("says so when the register does not know this organisation number", async () => {
    const fetchMock = registryFetch(null, (path, init) =>
      path === "/api/v1/customers/1001/registry-refresh" && init?.method === "POST"
        ? Promise.resolve(jsonResponse(200, { status: "unknown", changes: [] }))
        : null,
    );
    renderCard(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));

    expect(await screen.findByText("The register does not know this organisation number")).toBeInTheDocument();
  });

  it("says so when the entity has been removed from open data, whose record the server has just deleted", async () => {
    const fetchMock = registryFetch(null, (path, init) =>
      path === "/api/v1/customers/1001/registry-refresh" && init?.method === "POST"
        ? Promise.resolve(
            jsonResponse(200, {
              status: "removed",
              changes: [{ field: "removedFromOpenData", to: "2026-09-01" }],
            }),
          )
        : null,
    );
    renderCard(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));

    expect(await screen.findByText("Removed from the register")).toBeInTheDocument();
    // The inline note says why there is nothing below (task 4 review); the
    // generic "not fetched yet" line — which would say the wrong thing, this
    // was fetched, it was removed — is suppressed while it stands.
    expect(screen.getByText("This entity is no longer in the register's open data")).toBeInTheDocument();
    expect(screen.queryByText("Registry record not fetched yet")).not.toBeInTheDocument();
  });

  it("explains the 409 that means this customer has no organisation number to look up", async () => {
    const fetchMock = registryFetch(null, (path, init) =>
      path === "/api/v1/customers/1001/registry-refresh" && init?.method === "POST"
        ? Promise.resolve(
            problemResponse(409, {
              title: "No registry identity",
              detail: "This customer has no Norwegian organisation number.",
              code: "no_registry_identity",
            }),
          )
        : null,
    );
    renderCard(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));

    expect(
      await screen.findByText("There is no Norwegian organisation number on this customer to look up."),
    ).toBeInTheDocument();
  });

  it("says the registry could not be reached, and invites another try", async () => {
    const fetchMock = registryFetch(fullRecordBody, (path, init) =>
      path === "/api/v1/customers/1001/registry-refresh" && init?.method === "POST"
        ? Promise.resolve(problemResponse(502, { title: "Registry unavailable", detail: "Brreg could not be reached" }))
        : null,
    );
    renderCard(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));

    expect(await screen.findByText("Brønnøysundregistrene could not be reached. Try again.")).toBeInTheDocument();
    // The record on file stands: nothing was stored, so nothing is withdrawn.
    expect(screen.getByText("Organisasjonsledd")).toBeInTheDocument();
  });

  it("reports a name the register does not share with the legal identity, naming both sides", async () => {
    renderCard(registryFetch({ ...fullRecordBody, name: "REGISTERENHETEN I BRØNNØYSUND AS" }));

    expect(
      await screen.findByText(
        "The register calls this company REGISTERENHETEN I BRØNNØYSUND AS; the legal name here is REGISTERENHETEN I BRØNNØYSUND",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update legal name" })).toBeInTheDocument();
  });

  it("says nothing about the name when the two agree", async () => {
    renderCard(registryFetch(fullRecordBody));

    await screen.findByText("Organisasjonsledd");
    expect(screen.queryByText(/The register calls this company/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Update legal name" })).not.toBeInTheDocument();
  });

  // The comparison and the write both trim (task 3 review): a record name
  // that only differs from the legal name by surrounding whitespace is not a
  // real difference, and the notice never renders for one.
  it("says nothing about the name when the only difference is surrounding whitespace", async () => {
    renderCard(registryFetch({ ...fullRecordBody, name: `  ${identityBody.name}  ` }));

    await screen.findByText("Organisasjonsledd");
    expect(screen.queryByText(/The register calls this company/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Update legal name" })).not.toBeInTheDocument();
  });

  it("reports the name difference without offering the change to a caller who may not make it", async () => {
    renderCard(registryFetch({ ...fullRecordBody, name: "REGISTERENHETEN I BRØNNØYSUND AS" }), {
      canManageIdentity: false,
    });

    expect(
      await screen.findByText(
        "The register calls this company REGISTERENHETEN I BRØNNØYSUND AS; the legal name here is REGISTERENHETEN I BRØNNØYSUND",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Update legal name" })).not.toBeInTheDocument();
  });

  it("Update legal name writes the registry's name onto the identity, keeping everything else it asserts", async () => {
    const renamed = { ...fullRecordBody, name: "REGISTERENHETEN I BRØNNØYSUND AS" };
    const fetchMock = registryFetch(renamed, (path, init) =>
      path === "/api/v1/customers/1001/legal-identity" && init?.method === "PUT"
        ? Promise.resolve(jsonResponse(200, { ...identityBody, name: "REGISTERENHETEN I BRØNNØYSUND AS" }))
        : null,
    );
    const queryClient = renderCard(fetchMock);
    queryClient.setQueryData(["customers", 1001], { id: 1001, revision: 4 });

    await userEvent.click(await screen.findByRole("button", { name: "Update legal name" }));

    await waitFor(() => expect(lastIdentityPut(fetchMock)).toBeTruthy());
    const [, init] = lastIdentityPut(fetchMock) as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      country: "no",
      type: "business",
      id: "974760673",
      name: "REGISTERENHETEN I BRØNNØYSUND AS",
      source: "brreg",
    });
    // The PUT bumps the customer row's revision but answers the identity, not
    // the row: everything keyed on the customer is re-read rather than patched.
    await waitFor(() => expect(queryClient.getQueryState(["customers", 1001])?.isInvalidated).toBe(true));
  });

  // The write is trimmed even when the on-screen text carries the record's
  // untrimmed name (task 3 review): a leading/trailing space in the register's
  // own field must never reach the identity the server's own rule trims.
  it("Update legal name sends the registry's name trimmed, whatever whitespace the record itself carries", async () => {
    const renamed = { ...fullRecordBody, name: `  ${identityBody.name} AS  ` };
    const fetchMock = registryFetch(renamed, (path, init) =>
      path === "/api/v1/customers/1001/legal-identity" && init?.method === "PUT"
        ? Promise.resolve(jsonResponse(200, { ...identityBody, name: `${identityBody.name} AS` }))
        : null,
    );
    renderCard(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Update legal name" }));

    await waitFor(() => expect(lastIdentityPut(fetchMock)).toBeTruthy());
    const [, init] = lastIdentityPut(fetchMock) as [string, RequestInit];
    expect(JSON.parse(String(init.body)).name).toBe(`${identityBody.name} AS`);
  });

  // Task 10 review: the identity GET the write's own invalidation triggers is
  // what actually closes the notice — the mutation's own success handler never
  // seeds it, per this package's rule against seeding a form from
  // `invalidateQueries`'s resolution.
  it("the rename notice disappears once the refetched identity carries the new name", async () => {
    const renamed = { ...fullRecordBody, name: "REGISTERENHETEN I BRØNNØYSUND AS" };
    let identityName = identityBody.name;
    const fetchMock = registryFetch(renamed, (path, init) => {
      if (path === "/api/v1/customers/1001/legal-identity" && init?.method === "PUT") {
        identityName = "REGISTERENHETEN I BRØNNØYSUND AS";
        return Promise.resolve(jsonResponse(200, { ...identityBody, name: identityName }));
      }
      if (path === "/api/v1/customers/1001/legal-identity" && init?.method === undefined) {
        return Promise.resolve(jsonResponse(200, { ...identityBody, name: identityName }));
      }
      return null;
    });
    renderCard(fetchMock);

    expect(await screen.findByRole("button", { name: "Update legal name" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Update legal name" }));

    await waitFor(() => expect(screen.queryByText(/The register calls this company/)).not.toBeInTheDocument());
  });

  it("says the legal name could not be updated when the write fails", async () => {
    const renamed = { ...fullRecordBody, name: "REGISTERENHETEN I BRØNNØYSUND AS" };
    const fetchMock = registryFetch(renamed, (path, init) =>
      path === "/api/v1/customers/1001/legal-identity" && init?.method === "PUT"
        ? Promise.resolve(problemResponse(409, { title: "Duplicate legal identity", code: "duplicate_identity" }))
        : null,
    );
    renderCard(fetchMock);

    await userEvent.click(await screen.findByRole("button", { name: "Update legal name" }));

    expect(await screen.findByText("Legal name could not be updated")).toBeInTheDocument();
  });
});

/**
 * `foundedOn` and `deletedOn` are date-only strings with no time of their own
 * (task 1 review) — read in the reader's local zone rather than as UTC, a
 * reading west of Greenwich can land on the previous calendar day, exactly
 * the failure mode `dashboard.test.ts`'s own `formatInLosAngeles` stands in
 * for. That test injects a formatter into a pure function; this card's dates
 * go through `useI18n`'s own `formatters.formatDate`, which is not
 * injectable, so the reader's zone here is the process's own `TZ` — changed
 * for the one test that needs it and restored immediately after.
 */
// This package's tsconfig carries no Node types (it never otherwise needs
// them); declared locally rather than pulling in @types/node for one global.
declare const process: { env: Record<string, string | undefined> };

describe("date-only registry fields, read by a reader west of Greenwich", () => {
  const originalTZ = process.env.TZ;

  beforeEach(() => {
    process.env.TZ = "America/Los_Angeles";
  });

  afterEach(() => {
    process.env.TZ = originalTZ;
  });

  it("reads a deletion on 1 August as 1 August, not the 31st of July", async () => {
    renderCard(registryFetch({ ...fullRecordBody, deletedOn: "2026-08-01" }));

    expect(await screen.findByText("Deleted Aug 1, 2026")).toBeInTheDocument();
    expect(screen.queryByText(/Jul 31, 2026/)).not.toBeInTheDocument();
  });

  it("reads founded-on as the registry's own date, not the previous day", async () => {
    renderCard(registryFetch(fullRecordBody));

    expect(await screen.findByText("Aug 9, 1995")).toBeInTheDocument();
    expect(screen.queryByText("Aug 8, 1995")).not.toBeInTheDocument();
  });
});

/**
 * Where the card is actually mounted (design D5): the Overview tab shows it
 * only for a caller with `customers:legal-identity-view` and only for a
 * customer whose legal identity is a Norwegian business — the registry this
 * card reads holds nothing else. The tab also owns the record query, so the
 * addresses section below can offer the registry's addresses without a second
 * fetch of its own.
 */
describe("the Overview tab's Registry card", () => {
  const customerBody = (overrides: Record<string, unknown> = {}) => ({
    id: 1001,
    customerNumber: 5001,
    name: "Registerenheten",
    status: "active",
    type: "business",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    identity: { country: "no", type: "business", id: "974760673" },
    timelineSummary: { entryCount: 0, latestOccurredOn: null },
    revision: 1,
    contactInfo: {},
    ...overrides,
  });

  const overviewFetch = (customer: Record<string, unknown>) =>
    vi.fn((url: RequestInfo | URL) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001") return Promise.resolve(jsonResponse(200, customer));
      if (path === "/api/v1/customers/1001/legal-identity") return Promise.resolve(jsonResponse(200, identityBody));
      if (path === "/api/v1/customers/1001/billing-profile")
        return Promise.resolve(jsonResponse(200, { revision: 1, warnings: [] }));
      if (path === "/api/v1/customers/1001/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
      if (path === "/api/v1/customers/1001/registry-record") return Promise.resolve(jsonResponse(200, fullRecordBody));
      if (path.includes("/timeline")) return Promise.resolve(jsonResponse(200, { data: [], nextCursor: null }));
      if (path.includes("/contacts")) return Promise.resolve(jsonResponse(200, { data: [] }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });

  const renderOverview = async (
    fetchMock: ReturnType<typeof vi.fn>,
    props: { canViewIdentity?: boolean; canManageIdentity?: boolean; canEdit?: boolean },
  ) => {
    stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    // A router of its own: the Contacts card navigates, so the tab cannot be
    // rendered outside one.
    const router = createRouter({
      routeTree: createRootRoute({ component: () => <CustomerOverview customerId={1001} {...props} /> }),
      history: createMemoryHistory({ initialEntries: ["/"] }),
    });
    render(
      <MantineProvider env="test">
        <Notifications />
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <RouterProvider router={router} />
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>,
    );
    // The tab's own cards resolve the customer first; the registry is beside them.
    await screen.findByText("Contact & addresses", undefined, { timeout: 5000 });
  };

  it("shows the card for a Norwegian business when the caller may see the identity", async () => {
    const fetchMock = overviewFetch(customerBody());
    await renderOverview(fetchMock, { canViewIdentity: true, canManageIdentity: true });

    expect(await screen.findByRole("heading", { name: "Registry", level: 3 })).toBeInTheDocument();
    expect(await screen.findByText("Organisasjonsledd")).toBeInTheDocument();
  });

  it("hands the record down, so the registry's addresses are offered beside the customer's own", async () => {
    const fetchMock = overviewFetch(customerBody());
    await renderOverview(fetchMock, { canViewIdentity: true, canEdit: true });

    expect(await screen.findByRole("button", { name: "Use the registry's business address" })).toBeInTheDocument();
    // One query, one fetch: the card and the addresses section read the same key.
    expect(recordGets(fetchMock)).toHaveLength(1);
  });

  it("neither shows the card nor asks for the record without the view permission", async () => {
    const fetchMock = overviewFetch(customerBody());
    await renderOverview(fetchMock, { canViewIdentity: false, canEdit: true });

    expect(screen.queryByRole("heading", { name: "Registry" })).not.toBeInTheDocument();
    expect(recordGets(fetchMock)).toHaveLength(0);
    expect(screen.queryByRole("button", { name: /Use the registry/ })).not.toBeInTheDocument();
  });

  it("leaves the card out for a customer the registry has nothing to say about", async () => {
    // A private person, and a business whose identity is not Norwegian: the
    // Brønnøysund registry answers for neither.
    const person = overviewFetch(customerBody({ type: "person", identity: null }));
    await renderOverview(person, { canViewIdentity: true, canManageIdentity: true });
    expect(screen.queryByRole("heading", { name: "Registry" })).not.toBeInTheDocument();
    expect(recordGets(person)).toHaveLength(0);

    cleanup();
    const foreign = overviewFetch(customerBody({ identity: { country: "se", type: "business", id: "5560001234" } }));
    await renderOverview(foreign, { canViewIdentity: true, canManageIdentity: true });
    expect(screen.queryByRole("heading", { name: "Registry" })).not.toBeInTheDocument();
    expect(recordGets(foreign)).toHaveLength(0);
  });

  // Task 7 review: the customer's own `type` and its identity's own `type`
  // can disagree, and the record repeats the identity's organisation number,
  // not the customer's type — so it is the identity's own type that decides.
  it("leaves the card out when the identity itself is not a business, even though the customer is", async () => {
    const fetchMock = overviewFetch(customerBody({ identity: { country: "no", type: "person", id: "01010112345" } }));
    await renderOverview(fetchMock, { canViewIdentity: true, canManageIdentity: true });

    expect(screen.queryByRole("heading", { name: "Registry" })).not.toBeInTheDocument();
    expect(recordGets(fetchMock)).toHaveLength(0);
  });
});
