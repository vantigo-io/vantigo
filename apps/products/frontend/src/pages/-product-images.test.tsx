import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications, notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ProductImage } from "../api/products";
import { stubFetch } from "../test/fetch";
import { ProductImages } from "./-product-images";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const image = (id: number, sortOrder: number, extra: Partial<ProductImage> = {}): ProductImage => ({
  id,
  productId: 1,
  contentType: "image/jpeg",
  byteSize: 1000,
  width: 800,
  height: 600,
  sortOrder,
  isPrimary: sortOrder === 0,
  uses: ["communications"],
  createdAt: "",
  ...extra,
});

const first = image(10, 0, { altText: "The front" });
const second = image(11, 1, { uses: ["communications", "marketing"] });
const third = image(12, 2, { uses: [] });

/** Serves the list, and answers every other call with `answer`. */
const stubImages = (list: ProductImage[], answer: () => Response = () => jsonResponse(200, first)) =>
  stubFetch((url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === "/api/v1/products/1/images" && (init?.method ?? "GET") === "GET") {
      return Promise.resolve(jsonResponse(200, list));
    }
    return Promise.resolve(answer());
  });

const renderImages = () =>
  render(
    <MantineProvider>
      <ModalsProvider>
        <Notifications />
        <QueryClientProvider
          client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
        >
          <ProductImages productId={1} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );

const cards = () => screen.findAllByLabelText(/^Image \d+$/);
const writes = (fetchMock: ReturnType<typeof stubFetch>) =>
  fetchMock.actualCalls.filter(([, init]) => init?.method && init.method !== "GET");
/** The name of the file a recorded call sent as the part named file. */
const sentFile = ([, init]: [RequestInfo | URL, RequestInit | undefined]) =>
  (((init?.body as FormData | undefined)?.get("file") as File | null) ?? { name: "" }).name;
const bodyOf = (init?: RequestInit) => JSON.parse(String(init?.body));

describe("ProductImages", () => {
  afterEach(() => {
    cleanup();
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("says so when the product has no images", async () => {
    stubImages([]);
    renderImages();

    expect(await screen.findByText("No images yet.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Images" })).toBeInTheDocument();
  });

  it("shows the thumbnails in order, the star on the primary one, the alt text and the uses", async () => {
    stubImages([first, second, third]);
    renderImages();

    const [a, b, c] = await cards();
    expect(within(a).getByRole("img")).toHaveAttribute("src", "/api/v1/products/1/images/10/content?size=thumb");
    expect(within(a).getByRole("img")).toHaveAttribute("alt", "The front");
    expect(within(a).getByRole("button", { name: "Primary image" })).toBeDisabled();
    expect(within(b).getByRole("button", { name: "Make this the primary image" })).toBeEnabled();
    expect(within(a).getByLabelText("Alt text")).toHaveValue("The front");
    expect(within(a).getByRole("checkbox", { name: "Show in Communications" })).toBeChecked();
    expect(within(a).getByRole("checkbox", { name: "Show in marketing" })).not.toBeChecked();
    expect(within(b).getByRole("checkbox", { name: "Show in marketing" })).toBeChecked();
    expect(within(c).getByRole("checkbox", { name: "Show in Communications" })).not.toBeChecked();
    expect(screen.getAllByText("For marketing use, such as ads. Nothing reads it yet.")).toHaveLength(3);
    // The first cannot move left and the last cannot move right.
    expect(within(a).getByRole("button", { name: "Move left" })).toBeDisabled();
    expect(within(c).getByRole("button", { name: "Move right" })).toBeDisabled();
    expect(within(b).getByRole("button", { name: "Move left" })).toBeEnabled();
    expect(within(b).getByRole("button", { name: "Move right" })).toBeEnabled();
  });

  it("moves an image to the place before or after it", async () => {
    const fetchMock = stubImages([first, second, third]);
    renderImages();

    const [, b] = await cards();
    fireEvent.click(within(b).getByRole("button", { name: "Move left" }));
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(String(writes(fetchMock)[0][0])).toBe("/api/v1/products/1/images/11");
    expect(writes(fetchMock)[0][1]?.method).toBe("PATCH");
    expect(bodyOf(writes(fetchMock)[0][1])).toEqual({ sortOrder: 0 });

    fireEvent.click(within(b).getByRole("button", { name: "Move right" }));
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(2));
    expect(bodyOf(writes(fetchMock)[1][1])).toEqual({ sortOrder: 2 });
  });

  it("makes an image the primary one with its star", async () => {
    const fetchMock = stubImages([first, second]);
    renderImages();

    const [, b] = await cards();
    fireEvent.click(within(b).getByRole("button", { name: "Make this the primary image" }));
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(String(writes(fetchMock)[0][0])).toBe("/api/v1/products/1/images/11");
    expect(bodyOf(writes(fetchMock)[0][1])).toEqual({ isPrimary: true });
  });

  it("saves the alt text when the field is left, and only when it changed", async () => {
    const fetchMock = stubImages([first, second]);
    renderImages();

    const [a, b] = await cards();
    fireEvent.blur(within(a).getByLabelText("Alt text"));
    expect(writes(fetchMock)).toHaveLength(0);

    fireEvent.change(within(b).getByLabelText("Alt text"), { target: { value: "  A side view " } });
    fireEvent.blur(within(b).getByLabelText("Alt text"));
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(String(writes(fetchMock)[0][0])).toBe("/api/v1/products/1/images/11");
    expect(bodyOf(writes(fetchMock)[0][1])).toEqual({ altText: "A side view" });
  });

  it("sends the whole list of uses when a checkbox is changed", async () => {
    const fetchMock = stubImages([first, second]);
    renderImages();

    const [a, b] = await cards();
    fireEvent.click(within(a).getByRole("checkbox", { name: "Show in marketing" }));
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(bodyOf(writes(fetchMock)[0][1])).toEqual({ uses: ["communications", "marketing"] });

    fireEvent.click(within(b).getByRole("checkbox", { name: "Show in Communications" }));
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(2));
    expect(bodyOf(writes(fetchMock)[1][1])).toEqual({ uses: ["marketing"] });
  });

  it("asks before deleting an image", async () => {
    const fetchMock = stubImages([first, second], () => new Response(null, { status: 204 }));
    renderImages();

    const [, b] = await cards();
    fireEvent.click(within(b).getByRole("button", { name: "Delete image" }));
    expect(await screen.findByText("Delete this image? It is removed from the product for good.")).toBeInTheDocument();
    expect(writes(fetchMock)).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(String(writes(fetchMock)[0][0])).toBe("/api/v1/products/1/images/11");
    expect(writes(fetchMock)[0][1]?.method).toBe("DELETE");
  });

  it("uploads the files picked, each as a part named file", async () => {
    const fetchMock = stubImages([], () => jsonResponse(201, first));
    renderImages();

    const input = await screen.findByLabelText("Add images");
    const one = new File(["a"], "front.jpg", { type: "image/jpeg" });
    const two = new File(["b"], "back.png", { type: "image/png" });
    await userEvent.upload(input, [one, two]);

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(2));
    const [url, init] = writes(fetchMock)[0];
    expect(String(url)).toBe("/api/v1/products/1/images");
    expect(init?.method).toBe("POST");
    expect(sentFile([url, init])).toBe("front.jpg");
    expect(sentFile(writes(fetchMock)[1])).toBe("back.png");
  });

  it("uploads the files dropped", async () => {
    const fetchMock = stubImages([], () => jsonResponse(201, first));
    renderImages();

    const zone = (await screen.findByText(/Drop JPEG or PNG images here/)).closest("div")?.parentElement as HTMLElement;
    const file = new File(["a"], "front.jpg", { type: "image/jpeg" });
    fireEvent.drop(zone, { dataTransfer: { files: [file] } });

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(sentFile(writes(fetchMock)[0])).toBe("front.jpg");
  });

  it("sends a file the browser gave no type, for the server to judge", async () => {
    const fetchMock = stubImages([], () => jsonResponse(201, first));
    renderImages();

    const input = await screen.findByLabelText("Add images");
    fireEvent.change(input, { target: { files: [new File(["a"], "IMG_0001", { type: "" })] } });

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(sentFile(writes(fetchMock)[0])).toBe("IMG_0001");
  });

  it("holds the controls while a change is on its way", async () => {
    let release: (response: Response) => void = () => {};
    stubImages([first, second], () => new Response(null, { status: 500 }));
    renderImages();
    const [, b] = await cards();
    vi.stubGlobal(
      "fetch",
      vi.fn((_url: RequestInfo | URL, init?: RequestInit) =>
        init?.method === "PATCH"
          ? new Promise<Response>((resolve) => {
              release = resolve;
            })
          : Promise.resolve(jsonResponse(200, [first, second])),
      ),
    );

    fireEvent.click(within(b).getByRole("button", { name: "Move left" }));

    await waitFor(() => expect(within(b).getByRole("button", { name: "Delete image" })).toBeDisabled());
    expect(within(b).getByRole("checkbox", { name: "Show in marketing" })).toBeDisabled();
    release(jsonResponse(200, second));
    await waitFor(() => expect(within(b).getByRole("button", { name: "Delete image" })).toBeEnabled());
  });

  it("turns away a file that is no JPEG or PNG, or over 10 MiB, without sending it", async () => {
    const fetchMock = stubImages([]);
    renderImages();

    const input = await screen.findByLabelText("Add images");
    const webp = new File(["a"], "photo.webp", { type: "image/webp" });
    const huge = new File([new Uint8Array(10 * 1024 * 1024 + 1)], "huge.jpg", { type: "image/jpeg" });
    fireEvent.change(input, { target: { files: [webp, huge] } });

    expect(await screen.findByText("photo.webp is not a JPEG or PNG image.")).toBeInTheDocument();
    expect(screen.getByText("huge.jpg is larger than 10 MiB.")).toBeInTheDocument();
    expect(writes(fetchMock)).toHaveLength(0);
  });

  it.each([
    ["image_too_small", "The picture must be at least 200 by 200 pixels."],
    ["image_type_unsupported", "Only JPEG and PNG images can be uploaded."],
    ["image_too_large", "The picture is larger than 10 MiB, or has more than 40 megapixels."],
    ["image_invalid", "The file could not be read as a picture."],
  ])("tells the server's %s refusal by its code, against the file", async (code, sentence) => {
    stubImages([], () => jsonResponse(422, { title: "x", detail: "the server's own words", status: 422, code }));
    renderImages();

    await userEvent.upload(
      await screen.findByLabelText("Add images"),
      new File(["a"], "tiny.jpg", { type: "image/jpeg" }),
    );

    expect(await screen.findByText(`Could not upload tiny.jpg: ${sentence}`)).toBeInTheDocument();
  });

  it("says when the store is down", async () => {
    stubImages([], () => jsonResponse(503, { title: "x", status: 503, code: "storage_unavailable" }));
    renderImages();

    await userEvent.upload(
      await screen.findByLabelText("Add images"),
      new File(["a"], "front.jpg", { type: "image/jpeg" }),
    );

    expect(
      await screen.findByText(
        "Could not upload front.jpg: The image store could not be reached. Nothing was changed; try again.",
      ),
    ).toBeInTheDocument();
  });
});
