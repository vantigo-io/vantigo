import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PROFILE_QUERY_KEY } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { peopleWithRecords, withErased } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { type StaffStub, stubStaffApi } from "../test/server";

/** The photo writes the fake received, with their multipart bodies. */
const photoWrites = (fetchMock: StaffStub, method: string) =>
  fetchMock.actualCalls
    .filter(([url, init]) => (init?.method ?? "GET") === method && /\/people\/\d+\/photo$/.test(String(url)))
    .map(([url, init]) => ({ url: String(url), form: init?.body as FormData | undefined }));

/** The writes to identity's account avatar the fake received, with their multipart bodies. */
const avatarWrites = (fetchMock: StaffStub, method: string) =>
  fetchMock.actualCalls
    .filter(([url, init]) => (init?.method ?? "GET") === method && /\/identity\/account\/avatar$/.test(String(url)))
    .map(([url, init]) => ({ url: String(url), form: init?.body as FormData | undefined }));

/** Grace's user, whose own avatar the fixtures hold. */
const GRACE_USER = "8d5c6a3e-0000-4000-8000-000000000102";

/** The directory with Grace linked to her user, so her own avatar is her picture. */
const graceLinked = () =>
  peopleWithRecords().map((one) =>
    one.id === 102
      ? { ...one, record: { ...one.record, user: { id: GRACE_USER, displayName: "Grace at Fjord", active: true } } }
      : one,
  );

/** Opens the menu on the person's avatar and picks one of its items. */
const fromMenu = async (item: string, name = "Grace Hopper") => {
  await userEvent.click(await screen.findByRole("button", { name: `Photo of ${name}` }));
  await userEvent.click(await screen.findByRole("menuitem", { name: item }));
};

describe("The person's photo", () => {
  it("shows Grace's initials, who has no photo", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102");

    const grace = await screen.findByRole("img", { name: "Grace Hopper" });
    expect(grace).toHaveTextContent("GH");
  });

  it("shows Ada's photo, its address carrying when it was set", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    const ada = await screen.findByRole("img", { name: "Ada Lovelace" });
    expect(ada.tagName).toBe("IMG");
    expect(ada).toHaveAttribute("src", expect.stringContaining("/people/101/photo?v="));
  });

  it("lets HR change the photo, sent as one multipart file", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102");

    await fromMenu("Change photo");
    const photo = new File(["\x89PNG a photo"], "grace.png", { type: "image/png" });
    await userEvent.upload(screen.getByLabelText("Choose a file"), photo);

    await waitFor(() => expect(photoWrites(fetchMock, "PUT")).toHaveLength(1));
    const [{ url, form }] = photoWrites(fetchMock, "PUT");
    expect(url).toMatch(/\/api\/v1\/staff\/people\/102\/photo$/);
    expect(form).toBeInstanceOf(FormData);
    expect(((form as FormData).get("file") as File).name).toBe("grace.png");
    expect(fetchMock.state.photos[102]).toMatchObject({ contentType: "image/png" });
    expect(await screen.findByText("Photo changed")).toBeInTheDocument();
    const shown = await screen.findByRole("img", { name: "Grace Hopper" });
    await waitFor(() => expect(shown.tagName).toBe("IMG"));
    expect(screen.getByRole("img", { name: "Grace Hopper" })).toHaveAttribute(
      "src",
      expect.stringContaining("/people/102/photo?v="),
    );
  });

  it("says the server's refusal of a file that is not a JPEG or PNG", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102");

    await fromMenu("Change photo");
    // The picker offers JPEG and PNG; a browser that lets a GIF through is refused by the server.
    const gif = new File(["GIF89a"], "grace.gif", { type: "image/gif" });
    await userEvent.upload(screen.getByLabelText("Choose a file"), gif, { applyAccept: false });

    expect(await screen.findByText("The photo must be a JPEG or PNG")).toBeInTheDocument();
    expect(photoWrites(fetchMock, "PUT")).toHaveLength(1);
    expect(fetchMock.state.photos[102]).toBeUndefined();
    expect(screen.getByRole("img", { name: "Grace Hopper" })).toHaveTextContent("GH");
  });

  it("lets HR remove the photo after a confirmation, the initials back in its place", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    await fromMenu("Remove photo", "Ada Lovelace");
    const dialog = await screen.findByRole("dialog", { name: "Remove photo" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(photoWrites(fetchMock, "DELETE")).toHaveLength(1));
    expect(fetchMock.state.photos[101]).toBeUndefined();
    await waitFor(() => expect(screen.getByRole("img", { name: "Ada Lovelace" })).toHaveTextContent("AL"));
  });

  it("offers no Remove photo when there is none", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102");

    await userEvent.click(await screen.findByRole("button", { name: "Photo of Grace Hopper" }));
    expect(await screen.findByRole("menuitem", { name: "Change photo" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Remove photo" })).not.toBeInTheDocument();
  });

  it("offers the manager nothing to change", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102");

    expect(await screen.findByRole("img", { name: "Grace Hopper" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Photo of / })).not.toBeInTheDocument();
  });

  it("offers HR nothing to change on an erased record", async () => {
    stubStaffApi({ role: "manage", ...withErased() });
    renderRoute("/staff/people/106");

    expect(await screen.findByRole("img", { name: "Charles Babbage" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Photo of / })).not.toBeInTheDocument();
  });

  it("shows HR the user's own avatar when HR has set no photo, and offers no Remove photo of it", async () => {
    stubStaffApi({ role: "manage", people: graceLinked() });
    renderRoute("/staff/people/102");

    const grace = await screen.findByRole("img", { name: "Grace Hopper" });
    expect(grace.tagName).toBe("IMG");
    expect(grace).toHaveAttribute("src", expect.stringContaining("/people/102/photo?v=2026-08-15"));
    await userEvent.click(screen.getByRole("button", { name: "Photo of Grace Hopper" }));
    expect(await screen.findByRole("menuitem", { name: "Change photo" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Remove photo" })).not.toBeInTheDocument();
  });

  it("lets HR set its photo over the user's own avatar", async () => {
    const fetchMock = stubStaffApi({ role: "manage", people: graceLinked() });
    renderRoute("/staff/people/102");

    await fromMenu("Change photo");
    await userEvent.upload(
      screen.getByLabelText("Choose a file"),
      new File(["\x89PNG a photo"], "grace.png", { type: "image/png" }),
    );

    await waitFor(() => expect(photoWrites(fetchMock, "PUT")).toHaveLength(1));
    expect(avatarWrites(fetchMock, "POST")).toHaveLength(0);
    expect(fetchMock.state.photos[102]).toMatchObject({ contentType: "image/png" });
    // The user's own avatar is kept aside, not overwritten.
    expect(fetchMock.state.avatars[GRACE_USER]).toBeDefined();
    await waitFor(() =>
      expect(screen.getByRole("img", { name: "Grace Hopper" })).toHaveAttribute(
        "src",
        expect.stringContaining("/people/102/photo?v=2026-10-11T09"),
      ),
    );
  });

  it("says Set by HR on My page while HR's photo stands, and offers nothing to change", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    const menu = await screen.findByRole("button", { name: "Photo of Ada Lovelace" });
    expect(within(menu).getByRole("img", { name: "Ada Lovelace" })).toBeInTheDocument();
    await userEvent.click(menu);
    const setByHr = await screen.findByRole("menuitem", { name: /Set by HR/ });
    expect(setByHr).toHaveAttribute("aria-disabled", "true");
    expect(setByHr).toHaveTextContent("Ask HR to change it");
    expect(screen.queryByRole("menuitem", { name: "Change photo" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Remove photo" })).not.toBeInTheDocument();
  });

  it("lets the keyboard reach Set by HR, which does nothing", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    const menu = await screen.findByRole("button", { name: "Photo of Ada Lovelace" });
    menu.focus();
    await userEvent.keyboard("{Enter}");
    const setByHr = await screen.findByRole("menuitem", { name: /Set by HR/ });
    await userEvent.keyboard("{ArrowDown}");
    await waitFor(() => expect(setByHr).toHaveFocus());
    expect(setByHr).toHaveAttribute("aria-disabled", "true");
    await userEvent.keyboard("{Enter}");
    // The menu stays open, saying why, and nothing is sent.
    expect(screen.getByRole("menuitem", { name: /Set by HR/ })).toBeInTheDocument();
    expect(avatarWrites(fetchMock, "POST")).toHaveLength(0);
    expect(avatarWrites(fetchMock, "DELETE")).toHaveLength(0);
  });

  it("offers the person no menu on their own person page, only on My page", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/101");

    expect(await screen.findByRole("img", { name: "Ada Lovelace" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Photo of / })).not.toBeInTheDocument();
  });

  it("sends the person's own photo from My page to their Vantigo avatar", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 102, people: graceLinked() });
    renderRoute("/staff/me");

    const before = await screen.findByRole("img", { name: "Grace Hopper" });
    expect(before).toHaveAttribute("src", expect.stringContaining("/people/102/photo?v=2026-08-15"));
    await fromMenu("Change photo");
    const photo = new File(["\x89PNG a photo"], "grace.png", { type: "image/png" });
    await userEvent.upload(screen.getByLabelText("Choose a file"), photo);

    await waitFor(() => expect(avatarWrites(fetchMock, "POST")).toHaveLength(1));
    const [{ url, form }] = avatarWrites(fetchMock, "POST");
    expect(url).toMatch(/\/api\/v1\/identity\/account\/avatar$/);
    expect(((form as FormData).get("avatar") as File).name).toBe("grace.png");
    expect(photoWrites(fetchMock, "PUT")).toHaveLength(0);
    expect(fetchMock.state.photos[102]).toBeUndefined();
    expect(fetchMock.state.avatars[GRACE_USER]).toMatchObject({ contentType: "image/png", version: 2 });
    expect(await screen.findByText("Photo changed")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("img", { name: "Grace Hopper" })).toHaveAttribute(
        "src",
        expect.stringContaining("/people/102/photo?v=2026-10-11T10"),
      ),
    );
  });

  it("removes the person's own avatar from My page, the initials back in its place", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 102, people: graceLinked() });
    renderRoute("/staff/me");

    await fromMenu("Remove photo");
    const dialog = await screen.findByRole("dialog", { name: "Remove photo" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(avatarWrites(fetchMock, "DELETE")).toHaveLength(1));
    expect(photoWrites(fetchMock, "DELETE")).toHaveLength(0);
    expect(fetchMock.state.avatars[GRACE_USER]).toBeUndefined();
    await waitFor(() => expect(screen.getByRole("img", { name: "Grace Hopper" })).toHaveTextContent("GH"));
  });

  it("offers the person Change photo and no Remove photo when they have no picture", async () => {
    stubStaffApi({ role: "access", me: 102, people: graceLinked(), avatars: {} });
    renderRoute("/staff/me");

    await userEvent.click(await screen.findByRole("button", { name: "Photo of Grace Hopper" }));
    expect(await screen.findByRole("menuitem", { name: "Change photo" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Remove photo" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /Set by HR/ })).not.toBeInTheDocument();
  });

  it("reads the person and the shell's profile again after the person's own photo changed", async () => {
    stubStaffApi({ role: "access", me: 102, people: graceLinked() });
    const { queryClient } = renderRoute("/staff/me");
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    await fromMenu("Change photo");
    await userEvent.upload(
      screen.getByLabelText("Choose a file"),
      new File(["\x89PNG a photo"], "grace.png", { type: "image/png" }),
    );

    expect(await screen.findByText("Photo changed")).toBeInTheDocument();
    // The header's avatar is the host's profile query, under the shell's prefix.
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: PROFILE_QUERY_KEY }));
  });

  it("says Set by HR when HR's photo was set meanwhile, and the menu then says so too", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 102, people: graceLinked() });
    const { queryClient } = renderRoute("/staff/me");
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    await fromMenu("Change photo");
    // HR sets its photo while the person picks theirs: identity refuses.
    fetchMock.state.photos[102] = { contentType: "image/png", updatedAt: "2026-10-11T09:30:00Z" };
    await userEvent.upload(
      screen.getByLabelText("Choose a file"),
      new File(["\x89PNG a photo"], "grace.png", { type: "image/png" }),
    );

    expect(await screen.findByText("Ask HR to change it")).toBeInTheDocument();
    expect(screen.queryByText("Your photo is kept by Staff and cannot be changed here.")).not.toBeInTheDocument();
    expect(avatarWrites(fetchMock, "POST")).toHaveLength(1);
    expect(fetchMock.state.avatars[GRACE_USER]).toMatchObject({ version: 1 });
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: PROFILE_QUERY_KEY }));
    await waitFor(() =>
      expect(screen.getByRole("img", { name: "Grace Hopper" })).toHaveAttribute(
        "src",
        expect.stringContaining("/people/102/photo?v=2026-10-11T09%3A30"),
      ),
    );
    await userEvent.click(screen.getByRole("button", { name: "Photo of Grace Hopper" }));
    expect(await screen.findByRole("menuitem", { name: /Set by HR/ })).toHaveAttribute("aria-disabled", "true");
    expect(screen.queryByRole("menuitem", { name: "Change photo" })).not.toBeInTheDocument();
  });

  it("sends no DELETE when the remove is cancelled", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 102, people: graceLinked() });
    renderRoute("/staff/me");

    await fromMenu("Remove photo");
    const dialog = await screen.findByRole("dialog", { name: "Remove photo" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Remove photo" })).not.toBeInTheDocument());
    expect(avatarWrites(fetchMock, "DELETE")).toHaveLength(0);
    expect(fetchMock.state.avatars[GRACE_USER]).toBeDefined();
  });
});

describe("The photo as it is sent", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  /** A PNG of `size` bytes; its dimensions are what a test's stubbed decoder says. */
  const largePhoto = (size: number) => new File([new Uint8Array(size)], "grace.png", { type: "image/png" });

  it("scales a large photo down to 1024 px on its longer side, as a JPEG", async () => {
    const drawn: number[][] = [];
    vi.stubGlobal(
      "createImageBitmap",
      vi.fn(async () => ({ width: 4000, height: 3000, close: () => undefined })),
    );
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
      fillRect: () => undefined,
      drawImage: (_image: unknown, ...box: number[]) => drawn.push(box),
    } as unknown as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((callback, type) =>
      callback(new Blob(["scaled"], { type })),
    );
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102");

    await fromMenu("Change photo");
    await userEvent.upload(screen.getByLabelText("Choose a file"), largePhoto(3 * 1024 * 1024));

    await waitFor(() => expect(photoWrites(fetchMock, "PUT")).toHaveLength(1));
    const sent = photoWrites(fetchMock, "PUT")[0].form?.get("file") as File;
    expect(sent.name).toBe("grace.jpg");
    expect(sent.type).toBe("image/jpeg");
    expect(sent.size).toBe("scaled".length);
    expect(drawn).toEqual([[0, 0, 1024, 768]]);
    expect(fetchMock.state.photos[102]).toMatchObject({ contentType: "image/jpeg" });
  });

  it("sends a small photo as it is", async () => {
    vi.stubGlobal(
      "createImageBitmap",
      vi.fn(async () => ({ width: 600, height: 800, close: () => undefined })),
    );
    const toBlob = vi.spyOn(HTMLCanvasElement.prototype, "toBlob");
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102");

    await fromMenu("Change photo");
    await userEvent.upload(screen.getByLabelText("Choose a file"), largePhoto(200 * 1024));

    await waitFor(() => expect(photoWrites(fetchMock, "PUT")).toHaveLength(1));
    const sent = photoWrites(fetchMock, "PUT")[0].form?.get("file") as File;
    expect(sent.name).toBe("grace.png");
    expect(sent.size).toBe(200 * 1024);
    expect(toBlob).not.toHaveBeenCalled();
  });

  it("sends the original where the browser cannot decode it", async () => {
    // jsdom has no createImageBitmap: the guard leaves the file to the server.
    expect(typeof globalThis.createImageBitmap).toBe("undefined");
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102");

    await fromMenu("Change photo");
    await userEvent.upload(screen.getByLabelText("Choose a file"), largePhoto(3 * 1024 * 1024));

    await waitFor(() => expect(photoWrites(fetchMock, "PUT")).toHaveLength(1));
    const sent = photoWrites(fetchMock, "PUT")[0].form?.get("file") as File;
    expect(sent.name).toBe("grace.png");
    expect(sent.size).toBe(3 * 1024 * 1024);
  });
});
