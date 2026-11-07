import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ConversationMessage } from "../api/conversations";
import "../i18n";
import { noProductMessaging, type ProductMessaging, ProductMessagingProvider } from "../lib/product-messaging";
import { ChatMessage } from "./ChatMessage";

const navigate = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));

vi.mock("../api/conversations", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/conversations")>();
  return { ...actual, fetchInlineImage: vi.fn().mockResolvedValue("data:image/png;base64,AAAA") };
});

const participant = { id: "p-1", channelId: "c-1", address: "4242", displayName: "Kari Nordmann", contactId: null };

const message = (overrides: Partial<ConversationMessage>): ConversationMessage => ({
  id: "m-1",
  direction: "inbound",
  participant,
  authorUserId: null,
  subject: null,
  textBody: "Hello",
  htmlBody: null,
  occurredAt: "2026-01-01T10:00:00Z",
  createdAt: "2026-01-01T10:00:00Z",
  attachments: [],
  deliveries: [],
  ...overrides,
});

const show = (
  item: ConversationMessage,
  thread: ConversationMessage[] = [item],
  messaging: ProductMessaging = noProductMessaging,
  customerId: number | null = null,
) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MantineProvider>
        <ProductMessagingProvider value={messaging}>
          <ChatMessage message={item} messages={thread} customerId={customerId} />
        </ProductMessagingProvider>
      </MantineProvider>
    </QueryClientProvider>,
  );

describe("a message of a Messenger conversation", () => {
  beforeEach(() => void setLanguagePreference("en"));
  afterEach(cleanup);

  it("is a bubble with the participant's name", () => {
    show(message({}));
    expect(screen.getByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("Hello")).toBeInTheDocument();
  });

  it("marks the chosen answer option and shows the answer as a chip under its question", () => {
    const question = message({
      id: "q-1",
      direction: "outbound",
      participant: null,
      textBody: "Shall we?",
      components: {
        answerOptions: [
          { id: "yes", title: "Yes" },
          { id: "no", title: "No" },
        ],
      },
    });
    const answer = message({
      id: "a-1",
      textBody: "Yes",
      answerTo: { messageId: "q-1", choiceId: "yes", title: "Yes" },
    });
    const thread = [question, answer];

    const { container } = show(question, thread);
    expect(screen.getByText("Shall we?")).toBeInTheDocument();
    const chips = container.querySelectorAll(".mantine-Badge-root");
    expect(chips).toHaveLength(2);
    expect(chips[0]).toHaveAttribute("data-variant", "filled");
    expect(chips[1]).toHaveAttribute("data-variant", "outline");

    cleanup();
    show(answer, thread);
    // The answer is a chip with the title the customer saw, and not a bubble of plain text.
    expect(screen.getAllByText("Yes")).toHaveLength(1);
    expect(screen.getByText("Yes").closest(".mantine-Badge-root")).not.toBeNull();
  });

  it("says an e-mail address was asked for, and that one was shared", () => {
    show(message({ id: "q", direction: "outbound", participant: null, components: { shareEmail: true } }));
    expect(screen.getByText("Asked for an e-mail address")).toBeInTheDocument();
    cleanup();
    show(
      message({
        textBody: "kari@example.test",
        answerTo: { messageId: "q", choiceId: "share-email", title: "kari@example.test" },
      }),
    );
    expect(screen.getByText("kari@example.test")).toBeInTheDocument();
    expect(screen.getByText("Shared an e-mail address")).toBeInTheDocument();
  });

  it("marks an echo of the Page's own as sent from Meta", () => {
    show(message({ direction: "outbound", participant: null, echo: true, textBody: "From the suite" }));
    expect(screen.getByText("Sent from Meta")).toBeInTheDocument();
    expect(screen.queryByText("Sending")).not.toBeInTheDocument();
  });

  it("shows an image inline and a card's choices in a carousel, never listing a card's image as a file", async () => {
    show(
      message({
        direction: "outbound",
        participant: null,
        components: {
          cards: [
            { title: "Chair", subtitle: "100 kr", attachmentId: "att-card", choices: [{ id: "buy", title: "Buy" }] },
          ],
        },
        attachments: [
          {
            id: "att-photo",
            fileName: "photo.png",
            contentType: "image/png",
            sizeBytes: 10,
            contentId: null,
            scanStatus: "unscanned",
            isInline: false,
            createdAt: "2026-01-01T10:00:00Z",
            downloadAvailable: true,
            downloadPath: "/api/v1/communications/attachments/att-photo/download",
            role: "file",
          },
          {
            id: "att-card",
            fileName: "chair.png",
            contentType: "image/png",
            sizeBytes: 10,
            contentId: null,
            scanStatus: "clean",
            isInline: false,
            createdAt: "2026-01-01T10:00:00Z",
            downloadAvailable: true,
            downloadPath: "/api/v1/communications/attachments/att-card/download",
            role: "card",
          },
        ],
      }),
    );
    expect(await screen.findByAltText("photo.png")).toHaveAttribute("src", "data:image/png;base64,AAAA");
    expect(screen.getByText("Chair")).toBeInTheDocument();
    expect(screen.getByText("100 kr")).toBeInTheDocument();
    expect(screen.getByText("Buy")).toBeInTheDocument();
    expect(screen.queryByText("chair.png")).not.toBeInTheDocument();
  });

  describe("a Buy answer", () => {
    const cardImage = {
      id: "att-card",
      fileName: "chair.png",
      contentType: "image/png",
      sizeBytes: 10,
      contentId: null,
      scanStatus: "clean",
      isInline: false,
      createdAt: "2026-01-01T10:00:00Z",
      downloadAvailable: true,
      downloadPath: "/api/v1/communications/attachments/att-card/download",
      role: "card" as const,
    };
    const question = message({
      id: "q-1",
      direction: "outbound",
      participant: null,
      textBody: "Look",
      components: {
        cards: [{ title: "Chair", attachmentId: "att-card", choices: [{ id: "buy-0", title: "Buy" }] }],
      },
      attachments: [cardImage],
    });
    const buy = message({
      id: "a-1",
      textBody: "Buy",
      answerTo: {
        messageId: "q-1",
        choiceId: "buy-0",
        title: "Buy",
        product: { productId: 7, name: "Chair", attachmentId: "att-card" },
      },
    });

    it("shows as Wants to buy, with the card's image, and no link without Orders", async () => {
      show(buy, [question, buy]);
      expect(screen.getByText("Wants to buy: Chair")).toBeInTheDocument();
      await waitFor(() =>
        expect(document.querySelector("img.chat-card-image")).toHaveAttribute("src", "data:image/png;base64,AAAA"),
      );
      expect(screen.queryByRole("link", { name: "Create order" })).not.toBeInTheDocument();
    });

    it("carries a Create order link with the customer and the product when Orders is present", () => {
      const newOrder = vi.fn(({ customerId, productId }: { customerId: number | null; productId: number }) => ({
        to: "/orders/orders/new",
        search: { ...(customerId ? { customerId } : {}), productId },
      }));
      navigate.mockReset();
      show(buy, [question, buy], { canAttachProduct: true, canViewPricing: false, newOrder }, 1001);

      const link = screen.getByRole("link", { name: "Create order" });
      expect(newOrder).toHaveBeenCalledWith({ customerId: 1001, productId: 7 });
      expect(link.getAttribute("href")).toContain("/orders/orders/new?customerId=1001&productId=7");
      fireEvent.click(link);
      expect(navigate).toHaveBeenCalledWith({ to: "/orders/orders/new", search: { customerId: 1001, productId: 7 } });
    });

    it("is a plain answer when the choice is not Buy or the product is not known", () => {
      const other = message({
        id: "a-2",
        textBody: "Tell me more",
        answerTo: { messageId: "q-1", choiceId: "second", title: "Tell me more", product: null },
      });
      show(other, [question, other]);
      expect(screen.getByText("Tell me more").closest(".mantine-Badge-root")).not.toBeNull();
      expect(screen.queryByText(/Wants to buy/)).not.toBeInTheDocument();
    });
  });

  it("says where an outbound message stands, and why one failed", () => {
    const delivery = {
      id: "d-1",
      destination: "4242",
      recipientType: "to",
      attempts: 1,
      acceptedAt: null,
    };
    show(
      message({
        direction: "outbound",
        participant: null,
        deliveries: [{ ...delivery, status: "submission_failed", error: "reply_window_closed" }],
      }),
    );
    expect(screen.getByText(/Not sent/)).toBeInTheDocument();
    expect(screen.getByText(/the customer has to write again first/)).toBeInTheDocument();
    cleanup();
    show(
      message({ direction: "outbound", participant: null, deliveries: [{ ...delivery, status: "read", error: null }] }),
    );
    expect(screen.getByText("Read")).toBeInTheDocument();
  });
});
