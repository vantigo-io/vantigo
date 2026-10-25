import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { withErased } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { type StaffStub, stubStaffApi } from "../test/server";

/** The photo writes the fake received, with their multipart bodies. */
const photoWrites = (fetchMock: StaffStub, method: string) =>
  fetchMock.actualCalls
    .filter(([url, init]) => (init?.method ?? "GET") === method && /\/people\/\d+\/photo$/.test(String(url)))
    .map(([url, init]) => ({ url: String(url), form: init?.body as FormData | undefined }));

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

  it("lets the person change their own photo on My page", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    const menu = await screen.findByRole("button", { name: "Photo of Ada Lovelace" });
    expect(within(menu).getByRole("img", { name: "Ada Lovelace" })).toBeInTheDocument();
  });

  it("sends the person's own photo from My page to their record", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    await fromMenu("Change photo", "Ada Lovelace");
    const photo = new File(["\x89PNG a photo"], "ada.png", { type: "image/png" });
    await userEvent.upload(screen.getByLabelText("Choose a file"), photo);

    await waitFor(() => expect(photoWrites(fetchMock, "PUT")).toHaveLength(1));
    expect(photoWrites(fetchMock, "PUT")[0].url).toMatch(/\/api\/v1\/staff\/people\/101\/photo$/);
    expect(fetchMock.state.photos[101]).toMatchObject({ contentType: "image/png" });
  });

  it("sends no DELETE when the remove is cancelled", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    await fromMenu("Remove photo", "Ada Lovelace");
    const dialog = await screen.findByRole("dialog", { name: "Remove photo" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Remove photo" })).not.toBeInTheDocument());
    expect(photoWrites(fetchMock, "DELETE")).toHaveLength(0);
    expect(fetchMock.state.photos[101]).toBeDefined();
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
