import { Badge, Card, Group, ScrollArea, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type Attachment, fetchInlineImage, type MessageCard } from "../api/conversations";

/** One card's image as a data: URI, fetched by the page as an inline image is; nothing while it loads or when it cannot be shown. */
export function CardImage({ attachment }: { attachment: Attachment | undefined }) {
  const image = useQuery({
    queryKey: ["chat-card-image", attachment?.id],
    queryFn: ({ signal }) => fetchInlineImage(attachment?.downloadPath ?? "", signal),
    enabled: Boolean(attachment?.downloadAvailable),
    staleTime: Number.POSITIVE_INFINITY,
  });
  if (!image.data) return <div className="chat-card-image placeholder" />;
  return <img className="chat-card-image" src={image.data} alt="" />;
}

export type ChatCarouselProps = {
  cards: MessageCard[];
  /** The message's attachments: a card's image is the one of role "card" its attachmentId names. */
  attachments: Attachment[];
};

/**
 * A message's cards as a carousel (Messenger design N4, N8): each card's image, title, subtitle and its
 * choices, as the customer sees them — shown, not pressable, since the choice is the customer's. Built in
 * the groundwork and fed by the delivery that attaches a product; a message with no cards renders none.
 */
export function ChatCarousel({ cards, attachments }: ChatCarouselProps) {
  const { t } = useI18n("communications");
  if (cards.length === 0) return null;
  return (
    <ScrollArea type="auto" mt="xs" className="chat-carousel" aria-label={t("chatCarousel")}>
      <Group gap="sm" wrap="nowrap" align="stretch">
        {cards.map((card, index) => (
          <Card key={index} withBorder radius="md" padding="xs" className="chat-card">
            <CardImage attachment={attachments.find((file) => file.id === card.attachmentId)} />
            <Text fw={700} size="sm" mt="xs">
              {card.title}
            </Text>
            {card.subtitle && (
              <Text size="xs" c="dimmed">
                {card.subtitle}
              </Text>
            )}
            {card.choices && card.choices.length > 0 && (
              <Group gap={4} mt="xs">
                {card.choices.map((choice) => (
                  <Badge key={choice.id} variant="outline" size="sm">
                    {choice.title}
                  </Badge>
                ))}
              </Group>
            )}
          </Card>
        ))}
      </Group>
    </ScrollArea>
  );
}
