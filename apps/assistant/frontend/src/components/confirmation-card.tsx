import { Alert, Anchor, Badge, Button, Card, Group, Loader, Stack, Table, Text } from "@mantine/core";
import { IconAlertTriangle, IconCheck, IconClockOff, IconX } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useRef } from "react";
import type { AssistantPendingAction } from "../api/types";
import "../i18n";

export interface ConfirmationCardProps {
  card: AssistantPendingAction;
  /** The decision is being made: the buttons wait. */
  busy?: boolean;
  /** The card has just arrived: focus lands on the card, never on Confirm. */
  autoFocus?: boolean;
  /** A decision the server refused, in words. */
  error?: string;
  onConfirm: (card: AssistantPendingAction) => void;
  onCancel: (card: AssistantPendingAction) => void;
  onNavigate?: (route: string) => void;
}

const statusKey = {
  confirmed: "outcomeChanged",
  cancelled: "outcomeCancelled",
  expired: "outcomeExpired",
  failed: "outcomeFailed",
} as const;

const reasonKey: Record<string, string> = {
  changed: "reasonChanged",
  refused: "reasonRefused",
  failed: "reasonFailed",
  interrupted: "reasonInterrupted",
  stopped: "reasonStopped",
  expired: "reasonExpired",
};

const when = (iso: string) => {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
};

/**
 * A change the assistant proposed, waiting for the person (design S12): what
 * will happen, the record it is about as a link, the fields with their current
 * value and the new one where a read of the same record could say, and Confirm
 * or Cancel. Nothing has been written until Confirm. Once decided it shows how
 * it ended — Changed, Cancelled, Expired or Failed, with the reason — and no
 * values: the server keeps only the names of the fields after the decision.
 *
 * Enter does not confirm by accident: the card takes the focus when it
 * arrives, not its Confirm button, and the composer is off while it waits.
 */
export const ConfirmationCard = ({
  card,
  busy = false,
  autoFocus = false,
  error,
  onConfirm,
  onCancel,
  onNavigate,
}: ConfirmationCardProps) => {
  const { t } = useI18n("assistant");
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (autoFocus && card.status === "pending") ref.current?.focus();
  }, [autoFocus, card.status]);

  const pending = card.status === "pending";
  const making = card.status === "confirmed" && !card.outcome;
  const hasValues = card.fields.some((field) => field.next !== undefined);
  const hasCurrent = card.fields.some((field) => field.current !== undefined);
  const route = card.target?.route;
  const outcomeLabel =
    !pending && !making ? t(statusKey[card.status as keyof typeof statusKey] ?? "outcomeFailed") : "";
  const reason =
    card.outcome?.message ??
    (card.outcome?.reason && reasonKey[card.outcome.reason] ? t(reasonKey[card.outcome.reason]) : undefined);
  const failedColor = card.status === "failed" ? "red" : card.status === "confirmed" ? "teal" : "gray";

  return (
    <Card
      ref={ref}
      withBorder
      padding="sm"
      radius="md"
      tabIndex={-1}
      role="group"
      aria-label={t("cardLabel")}
      data-testid="confirmation-card"
      data-status={card.status}
      style={{ outline: "none" }}
    >
      <Stack gap="xs">
        <Group justify="space-between" wrap="nowrap" align="flex-start" gap="xs">
          <Stack gap={2} flex={1}>
            <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
              {t("cardLabel")}
            </Text>
            <Text size="sm" fw={600}>
              {card.explain || card.summary}
            </Text>
            {card.explain && card.explain !== card.summary && (
              <Text size="xs" c="dimmed">
                {card.summary}
              </Text>
            )}
          </Stack>
          {!pending && !making && (
            <Badge
              color={failedColor}
              variant="light"
              leftSection={
                card.status === "confirmed" ? (
                  <IconCheck size={12} />
                ) : card.status === "expired" ? (
                  <IconClockOff size={12} />
                ) : card.status === "failed" ? (
                  <IconAlertTriangle size={12} />
                ) : (
                  <IconX size={12} />
                )
              }
            >
              {outcomeLabel}
            </Badge>
          )}
          {making && (
            <Group gap={4} c="dimmed" wrap="nowrap" role="status">
              <Loader size={12} />
              <Text size="xs" c="dimmed">
                {t("making")}
              </Text>
            </Group>
          )}
        </Group>

        {card.target && (card.target.label || route) && (
          <Text size="sm">
            <Text span c="dimmed">
              {t("cardTarget")}{" "}
            </Text>
            {route ? (
              <Anchor
                href={route}
                size="sm"
                onClick={(event) => {
                  if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
                  event.preventDefault();
                  onNavigate?.(route);
                }}
              >
                {card.target.label || route}
              </Anchor>
            ) : (
              card.target.label
            )}
          </Text>
        )}

        {card.fields.length > 0 &&
          (hasValues ? (
            <Table withTableBorder fz="xs" verticalSpacing={4}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("cardFieldColumn")}</Table.Th>
                  {hasCurrent && <Table.Th>{t("cardNowColumn")}</Table.Th>}
                  <Table.Th>{t("cardNewColumn")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {card.fields.map((field) => (
                  <Table.Tr key={field.name}>
                    <Table.Td>{field.name}</Table.Td>
                    {hasCurrent && (
                      <Table.Td c="dimmed">{field.current === undefined ? "—" : field.current || "—"}</Table.Td>
                    )}
                    <Table.Td fw={500}>{field.next || "—"}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          ) : (
            <Text size="xs" c="dimmed">
              {t("cardFields", { fields: card.fields.map((field) => field.name).join(", ") })}
            </Text>
          ))}

        {pending && (
          <>
            <Text size="xs" c="dimmed">
              {t("cardExpires", { time: when(card.expiresAt) })}
            </Text>
            <Group gap="xs">
              <Button size="xs" loading={busy} onClick={() => onConfirm(card)}>
                {t("confirm")}
              </Button>
              <Button size="xs" variant="default" disabled={busy} onClick={() => onCancel(card)}>
                {t("cancel")}
              </Button>
            </Group>
          </>
        )}
        {!pending && !making && reason && (
          <Text size="xs" c={card.status === "failed" ? "red" : "dimmed"}>
            {reason}
          </Text>
        )}
        {error && (
          <Alert color="red" p="xs" role="alert">
            {error}
          </Alert>
        )}
      </Stack>
    </Card>
  );
};
