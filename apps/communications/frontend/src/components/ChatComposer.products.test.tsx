import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ConversationDetail } from "../api/conversations";
import "../i18n";
import { noProductMessaging, type ProductMessaging, ProductMessagingProvider } from "../lib/product-messaging";
import { ChatComposer } from "./ChatComposer";

const mocks = vi.hoisted(() => ({ replyToConversation: vi.fn() }));

vi.mock("../api/conversations", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/conversations")>();
  return {
    ...actual,
    replyToConversation: mocks.replyToConversation,
    addConversationNote: vi.fn(),
    stageConversationAttachment: vi.fn(),
    attachmentUploadStatusQueryOptions: () => ({ queryKey: ["upload"], queryFn: () => undefined, enabled: false }),
  };
});

const product = (id: number, name: string, amount?: number) => ({
  id,
  name,
  sku: `SKU-${id}`,
  status: "Active",
  effectivePrices: amount === undefined ? [] : [{ amount, currency: "NOK" }],
  variants: [],
});
const image = (id: number, productId: number, order: number, uses: string[]) => ({
  id,
  productId,
  isPrimary: order === 0,
  sortOrder: order,
  uses,
});

// Chair has three images, two marked for Communications; Lamp has one, marked for marketing only.
const imagesOf: Record<number, ReturnType<typeof image>[]> = {
  7: [image(72, 7, 1, ["communications"]), image(71, 7, 0, ["communications"]), image(73, 7, 2, ["marketing"])],
  8: [image(81, 8, 0, ["marketing"])],
};

vi.mock("../api/products", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/products")>();
  return {
    ...actual,
    pickerProductsQueryOptions: (search: string) => ({
      queryKey: ["picker-products", search],
      queryFn: async () => [product(7, "Chair", 1200), product(8, "Lamp", 300)],
    }),
    pickerImagesQueryOptions: (productId: number) => ({
      queryKey: ["picker-images", productId],
      queryFn: async () => imagesOf[productId] ?? [],
    }),
  };
});

const detail = (): ConversationDetail =>
  ({
    id: "conversation-1",
    channelId: "channel-1",
    channelType: "messenger",
    customerId: 1001,
    messages: [],
    replyRecipients: { canReply: true, canReplyAll: false, replyTo: "4242", replyAllCc: [] },
    componentLimits: {
      maxText: 2000,
      maxAnswerOptions: 13,
      maxAnswerTitle: 20,
      shareEmail: true,
      maxCards: 10,
      maxCardChoices: 3,
      maxCardTitle: 80,
      maxCardSubtitle: 80,
      maxChoiceTitle: 20,
    },
    replyWindow: { state: "open", closesAt: "2026-01-02T00:00:00Z" },
  }) as unknown as ConversationDetail;

const renderComposer = (messaging: ProductMessaging) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MantineProvider>
        <ProductMessagingProvider value={messaging}>
          <ChatComposer conversation={detail()} onSent={vi.fn().mockResolvedValue(undefined)} />
        </ProductMessagingProvider>
      </MantineProvider>
    </QueryClientProvider>,
  );

const allowed: ProductMessaging = { canAttachProduct: true, canViewPricing: true };
const send = () => screen.getByRole("button", { name: "Send reply" });
const write = (text: string) => fireEvent.change(screen.getByLabelText("Message"), { target: { value: text } });

const attachChair = async () => {
  fireEvent.click(screen.getByRole("button", { name: "Attach product" }));
  fireEvent.click(await screen.findByRole("button", { name: "Chair" }));
  return screen.findByLabelText("Attached product");
};

describe("Attach product", () => {
  beforeEach(() => {
    void setLanguagePreference("en");
    mocks.replyToConversation.mockReset().mockResolvedValue({ conversationId: "conversation-1", messageId: "m-1" });
  });
  afterEach(cleanup);

  it("is hidden without the Products module and products:products-view", () => {
    renderComposer(noProductMessaging);
    expect(screen.queryByRole("button", { name: "Attach product" })).not.toBeInTheDocument();
  });

  it("lists the products, saying why one cannot be attached when no image is marked for Communications", async () => {
    renderComposer(allowed);
    fireEvent.click(screen.getByRole("button", { name: "Attach product" }));

    const chair = await screen.findByRole("button", { name: "Chair" });
    expect(within(chair).getByText(/Images: 2/)).toBeInTheDocument();
    const lamp = screen.getByRole("button", { name: "Lamp" });
    await waitFor(() => expect(lamp).toBeDisabled());
    expect(within(lamp).getByText(/No image is marked for Communications/)).toBeInTheDocument();
  });

  it("makes a card of each image marked for Communications, in their order, with the price as the subtitle", async () => {
    renderComposer(allowed);
    const draft = await attachChair();

    const subtitles = within(draft).getAllByRole("textbox", { name: /Subtitle of card/ });
    expect(subtitles).toHaveLength(2);
    expect((subtitles[0] as HTMLInputElement).value).toMatch(/1\D?200.*ex VAT$/);
    expect(within(draft).getAllByText("Chair")).toHaveLength(2);
    expect(within(draft).getByRole("textbox", { name: "Buy button" })).toHaveValue("Buy");
  });

  it("leaves the subtitle empty without products:pricing-view", async () => {
    renderComposer({ canAttachProduct: true, canViewPricing: false });
    const draft = await attachChair();
    for (const subtitle of within(draft).getAllByRole("textbox", { name: /Subtitle of card/ }))
      expect(subtitle).toHaveValue("");
  });

  it("sends the cards: each names its image, with Buy and the second choice, and an edited subtitle", async () => {
    renderComposer(allowed);
    const draft = await attachChair();
    fireEvent.change(within(draft).getByRole("textbox", { name: "Subtitle of card 1" }), {
      target: { value: "Sale!" },
    });
    fireEvent.change(within(draft).getByRole("textbox", { name: "Second button" }), {
      target: { value: "Tell me more" },
    });
    fireEvent.change(within(draft).getByRole("textbox", { name: "Subtitle of card 2" }), { target: { value: "" } });
    write("Look at this");
    fireEvent.click(send());

    await waitFor(() => expect(mocks.replyToConversation).toHaveBeenCalledTimes(1));
    const [, body] = mocks.replyToConversation.mock.calls[0] ?? [];
    expect(body.components.cards).toEqual([
      {
        title: "Chair",
        subtitle: "Sale!",
        productImageId: 71,
        choices: [
          { id: "buy", title: "Buy" },
          { id: "second", title: "Tell me more" },
        ],
      },
      {
        title: "Chair",
        productImageId: 72,
        choices: [
          { id: "buy", title: "Buy" },
          { id: "second", title: "Tell me more" },
        ],
      },
    ]);
  });

  it("titles the Buy button Kjøp in Norwegian", async () => {
    void setLanguagePreference("nb");
    renderComposer(allowed);
    fireEvent.click(await screen.findByRole("button", { name: "Legg ved produkt" }));
    fireEvent.click(await screen.findByRole("button", { name: "Chair" }));
    const draft = await screen.findByLabelText("Lagt ved produkt");
    expect(within(draft).getByRole("textbox", { name: "Kjøp-knapp" })).toHaveValue("Kjøp");
  });

  it("lets the agent remove a card, and the product with its last", async () => {
    renderComposer(allowed);
    const draft = await attachChair();

    fireEvent.click(within(draft).getByRole("button", { name: "Remove card 1" }));
    expect(within(draft).getAllByRole("textbox", { name: /Subtitle of card/ })).toHaveLength(1);

    fireEvent.click(within(draft).getByRole("button", { name: "Remove card 1" }));
    expect(screen.queryByLabelText("Attached product")).not.toBeInTheDocument();
    write("Hi");
    fireEvent.click(send());
    await waitFor(() => expect(mocks.replyToConversation).toHaveBeenCalledTimes(1));
    expect(mocks.replyToConversation.mock.calls[0]?.[1].components).toBeUndefined();
  });

  it("holds sending back for a subtitle over the limit", async () => {
    renderComposer(allowed);
    const draft = await attachChair();
    write("Hi");
    expect(send()).toBeEnabled();
    fireEvent.change(within(draft).getByRole("textbox", { name: "Subtitle of card 1" }), {
      target: { value: "x".repeat(81) },
    });
    expect(send()).toBeDisabled();
  });
});
