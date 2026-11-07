import { ActionIcon, Card, Group, ScrollArea, Text, TextInput } from "@mantine/core";
import { IconX } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { ComponentLimits } from "../api/conversations";
import { characters } from "./chat";
import type { ProductDraft } from "./productDraft";

export type ProductDraftEditorProps = {
  draft: ProductDraft;
  limits: ComponentLimits;
  onChange: (draft: ProductDraft) => void;
  /** Removes the whole product from the message. */
  onRemove: () => void;
};

/**
 * The carousel of a product being attached, as the customer will see it (Messenger design N16): a card per
 * image marked for Communications, each with a × to leave it out, a subtitle the agent may edit or clear,
 * and the choices every card carries — *Buy* and an optional second one the agent writes.
 */
export function ProductDraftEditor({ draft, limits, onChange, onRemove }: ProductDraftEditorProps) {
  const { t } = useI18n("communications");
  const setCard = (key: string, subtitle: string) =>
    onChange({ ...draft, cards: draft.cards.map((card) => (card.key === key ? { ...card, subtitle } : card)) });
  const removeCard = (key: string) => {
    const cards = draft.cards.filter((card) => card.key !== key);
    if (cards.length === 0) onRemove();
    else onChange({ ...draft, cards });
  };
  const buyLength = characters(draft.buyTitle.trim());
  const secondLength = characters(draft.secondTitle.trim());
  return (
    <Card withBorder radius="md" padding="sm" mt="xs" className="product-draft" aria-label={t("attachedProduct")}>
      <Group justify="space-between" mb="xs">
        <Text size="sm" fw={600}>
          {t("attachedProductName", { name: draft.productName })}
        </Text>
        <ActionIcon variant="subtle" color="gray" size="sm" aria-label={t("removeProduct")} onClick={onRemove}>
          <IconX size={14} />
        </ActionIcon>
      </Group>
      <ScrollArea type="auto">
        <Group gap="sm" wrap="nowrap" align="stretch">
          {draft.cards.map((card, index) => {
            const used = characters(card.subtitle);
            const over = used > limits.maxCardSubtitle;
            return (
              <Card key={card.key} withBorder radius="md" padding="xs" className="chat-card">
                <Group justify="flex-end" mb={2}>
                  <ActionIcon
                    variant="subtle"
                    color="gray"
                    size="xs"
                    aria-label={t("removeCard", { n: index + 1 })}
                    onClick={() => removeCard(card.key)}
                  >
                    <IconX size={12} />
                  </ActionIcon>
                </Group>
                <img className="chat-card-image" src={card.thumbnailUrl} alt="" />
                <Text fw={700} size="sm" mt="xs">
                  {card.title}
                </Text>
                <TextInput
                  size="xs"
                  mt={4}
                  aria-label={t("cardSubtitleN", { n: index + 1 })}
                  placeholder={t("cardSubtitle")}
                  value={card.subtitle}
                  error={over ? t("cardSubtitleTooLong", { max: limits.maxCardSubtitle }) : undefined}
                  rightSectionWidth={52}
                  rightSection={
                    <Text size="xs" c={over ? "red" : "dimmed"}>
                      {used}/{limits.maxCardSubtitle}
                    </Text>
                  }
                  onChange={(event) => setCard(card.key, event.currentTarget.value)}
                />
              </Card>
            );
          })}
        </Group>
      </ScrollArea>
      <Group gap="sm" mt="sm" align="flex-start" grow>
        <TextInput
          size="xs"
          label={t("buyButton")}
          value={draft.buyTitle}
          error={
            buyLength < 1 || buyLength > limits.maxChoiceTitle
              ? t("choiceTitleBounds", { max: limits.maxChoiceTitle })
              : undefined
          }
          onChange={(event) => onChange({ ...draft, buyTitle: event.currentTarget.value })}
        />
        <TextInput
          size="xs"
          label={t("secondButton")}
          placeholder={t("secondButtonPlaceholder")}
          value={draft.secondTitle}
          error={
            secondLength > limits.maxChoiceTitle ? t("choiceTitleBounds", { max: limits.maxChoiceTitle }) : undefined
          }
          onChange={(event) => onChange({ ...draft, secondTitle: event.currentTarget.value })}
        />
      </Group>
    </Card>
  );
}
