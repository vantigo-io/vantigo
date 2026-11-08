import {
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Code,
  Group,
  List,
  Modal,
  NativeSelect,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconClock, IconLock, IconMessageCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { formatDate, UserAvatar, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import {
  approveItem,
  discardItem,
  reviewCountQueryKey,
  reviewItemQueryKey,
  reviewQueryKey,
  sendEditedItem,
} from "../api/agents";
import { errorCode, isVersionConflict } from "../api/request";
import type { DiscardReason, ReviewItem } from "../api/types";
import { discardReasons, heldKey, isUrgent, msUntil, outcomeKey, remaining } from "../lib/review";
import { runLinkOptions } from "../lib/routes";
import { useDecisionError, useRemainingText } from "./use-decision-error";

/** Re-renders every 30 seconds, so a countdown stays honest while the page is open. */
const useNow = (): number => {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(id);
  }, []);
  return now;
};

/** What a decided or withdrawn item came to, in words. */
const stateKey = (item: ReviewItem): string => `state.${item.state}`;

/**
 * One review item (V20, screen 4): why it waits, the score and the sources, the
 * window's countdown, the reply with the disclosure locked above it, and the
 * ways to decide: Send, Edit and send, Discard with a reason. A decision names
 * the version it read; a stale one reloads the item and says so.
 */
export const ReviewItemCard = ({ item, canReview }: { item: ReviewItem; canReview: boolean }) => {
  const { t } = useI18n("agents");
  const decisionError = useDecisionError();
  const remainingText = useRemainingText();
  const queryClient = useQueryClient();
  const now = useNow();
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(item.reply?.text ?? "");
  const [discarding, setDiscarding] = useState(false);
  const [reason, setReason] = useState<DiscardReason>("wrong_facts");
  const [why, setWhy] = useState("");

  const waiting = item.state === "waiting";
  const decide = canReview && waiting;
  const left = msUntil(item.expiresAt, now);
  const windowLeft = msUntil(item.windowClosesAt, now);
  const urgent = waiting && isUrgent(item.expiresAt, item.windowClosesAt, now);

  const settle = (saved: ReviewItem, title: string) => {
    queryClient.setQueryData(reviewItemQueryKey(item.id), saved);
    void queryClient.invalidateQueries({ queryKey: reviewQueryKey });
    void queryClient.invalidateQueries({ queryKey: reviewCountQueryKey });
    notifications.show({ color: saved.state === "withdrawn" ? "yellow" : "teal", title, message: "" });
    setEditing(false);
    setDiscarding(false);
  };
  const fail = (error: unknown) => {
    if (isVersionConflict(error) || errorCode(error) === "item_decided") {
      notifications.show({ color: "yellow", title: t("staleTitle"), message: t("itemStaleBody") });
    } else {
      notifications.show({ color: "red", title: t("decisionFailed"), message: decisionError(error) });
    }
    void queryClient.invalidateQueries({ queryKey: reviewItemQueryKey(item.id) });
    void queryClient.invalidateQueries({ queryKey: reviewQueryKey });
  };
  const send = useMutation({
    mutationFn: () => (editing ? sendEditedItem(item, text.trim()) : approveItem(item)),
    onSuccess: (saved) => settle(saved, saved.state === "withdrawn" ? t("itemWithdrawnNow") : t("itemSent")),
    onError: fail,
  });
  const discard = useMutation({
    mutationFn: () => discardItem(item, reason, why),
    onSuccess: (saved) => settle(saved, t("itemDiscarded")),
    onError: fail,
  });

  const reply = item.reply;
  const change = item.change;
  return (
    <Card withBorder padding="md" radius="md" data-testid="review-item" data-state={item.state}>
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <Group gap="xs" wrap="nowrap">
            <UserAvatar kind="agent" name={item.agentName} size="md" />
            <div>
              <Text fw={600}>{item.agentName}</Text>
              <Text size="xs" c="dimmed">
                {item.roleName ?? t("roleGone")} ·{" "}
                {formatDate(item.createdAt, { dateStyle: "medium", timeStyle: "short" })}
              </Text>
            </div>
          </Group>
          <Group gap={6}>
            <Badge variant="light" color={item.kind === "reply" ? "blue" : item.kind === "start" ? "teal" : "grape"}>
              {item.kind === "reply" ? t("kindReply") : item.kind === "start" ? t("kindStart") : t("kindChange")}
            </Badge>
            {!waiting && (
              <Badge variant="outline" color={item.state === "discarded" ? "red" : "gray"}>
                {t(stateKey(item))}
              </Badge>
            )}
          </Group>
        </Group>

        {waiting && (
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              {t("whyItWaits")}
            </Text>
            {item.heldBy.length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("whyMode")}
              </Text>
            ) : (
              <List size="sm" spacing={2} data-testid="held-by">
                {item.heldBy.map((code) => (
                  <List.Item key={code}>{t(heldKey(code))}</List.Item>
                ))}
              </List>
            )}
          </Stack>
        )}

        <Group gap="md">
          <Badge variant="light" color={item.score >= 85 ? "teal" : item.score >= 60 ? "yellow" : "red"}>
            {t("scoreBadge", { score: item.score })}
          </Badge>
          {item.modelScore !== item.score && (
            <Text size="xs" c="dimmed">
              {t("modelScoreNote", { score: item.modelScore })}
            </Text>
          )}
          {waiting && left !== null && (
            <Group gap={4} wrap="nowrap" c={urgent ? "orange" : "dimmed"}>
              <IconClock size={14} aria-hidden />
              <Text size="xs" c="inherit" data-testid="countdown">
                {windowLeft !== null
                  ? t("windowCloses", { time: remainingText(remaining(windowLeft)) })
                  : t("lapsesIn", { time: remainingText(remaining(left)) })}
              </Text>
            </Group>
          )}
        </Group>

        <Stack gap={2}>
          <Text size="sm" fw={500}>
            {t("sources")}
          </Text>
          {item.sources.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noSources")}
            </Text>
          ) : (
            <List size="sm" spacing={2}>
              {item.sources.map((source) => (
                <List.Item key={source}>
                  <Code>{source}</Code>
                </List.Item>
              ))}
            </List>
          )}
        </Stack>

        {item.start && (
          <Stack gap={2} data-testid="start">
            <Text size="sm">{t("startTo", { to: item.start.to })}</Text>
            <Text size="sm" c="dimmed">
              {t("startSubject", { subject: item.start.subject })}
            </Text>
          </Stack>
        )}

        {reply && (
          <Stack gap={6}>
            <TextInput
              aria-label={t("disclosureLocked")}
              description={t("disclosureLockedHelp")}
              value={reply.disclosure}
              readOnly
              leftSection={<IconLock size={14} aria-hidden />}
            />
            {editing ? (
              <Textarea
                aria-label={t("replyText")}
                value={text}
                onChange={(event) => setText(event.currentTarget.value)}
                autosize
                minRows={4}
                maxRows={16}
                data-autofocus
              />
            ) : (
              <Text style={{ whiteSpace: "pre-wrap" }} data-testid="reply-text">
                {reply.text}
              </Text>
            )}
          </Stack>
        )}

        {change && (
          <Stack gap={4}>
            <Text fw={500}>{change.summary}</Text>
            {change.explain !== "" && <Text size="sm">{change.explain}</Text>}
            <Text size="xs" c="dimmed">
              <Code>{change.operationId}</Code>
            </Text>
            {change.body && <Code block>{JSON.stringify(change.body, null, 2)}</Code>}
          </Stack>
        )}

        {(item.state === "withdrawn" || item.state === "expired") && item.outcome && (
          <Text size="sm" c="dimmed" data-testid="outcome">
            {t(outcomeKey(item.outcome))}
          </Text>
        )}

        {item.discardReason && (
          <Text size="sm" c="dimmed">
            {t(`discard.${item.discardReason}`)}
            {item.discardText ? `: ${item.discardText}` : ""}
          </Text>
        )}

        {decide && (
          <Group justify="space-between">
            <Group gap="xs">
              <Button loading={send.isPending} disabled={editing && text.trim() === ""} onClick={() => send.mutate()}>
                {editing ? t("sendEdited") : item.kind !== "change" ? t("send") : t("approve")}
              </Button>
              {reply &&
                (editing ? (
                  <Button
                    variant="default"
                    onClick={() => {
                      setEditing(false);
                      setText(reply.text);
                    }}
                  >
                    {t("cancel")}
                  </Button>
                ) : (
                  <Button variant="default" onClick={() => setEditing(true)}>
                    {t("editAndSend")}
                  </Button>
                ))}
            </Group>
            <Button variant="subtle" color="red" onClick={() => setDiscarding(true)}>
              {t("discard")}
            </Button>
          </Group>
        )}
        {waiting && !canReview && <Alert color="gray">{t("noReviewPermission")}</Alert>}

        <Group gap="md">
          {item.kind !== "start" && (
            <Anchor
              size="xs"
              renderRoot={(props) => (
                <Link to="/communications/inbox" search={{ conversationId: item.conversationId }} {...props} />
              )}
            >
              <Group gap={4} wrap="nowrap">
                <IconMessageCircle size={12} aria-hidden />
                {t("openConversation")}
              </Group>
            </Anchor>
          )}
          <Anchor size="xs" renderRoot={(props) => <Link {...runLinkOptions(item.runId)} {...props} />}>
            {t("openRun")}
          </Anchor>
        </Group>
      </Stack>

      <Modal opened={discarding} onClose={() => setDiscarding(false)} title={t("discardTitle")}>
        <Stack gap="sm">
          <NativeSelect
            label={t("discardReason")}
            value={reason}
            onChange={(event) => setReason(event.currentTarget.value as DiscardReason)}
            data={discardReasons.map((value) => ({ value, label: t(`discard.${value}`) }))}
          />
          <Textarea
            label={t("discardText")}
            value={why}
            onChange={(event) => setWhy(event.currentTarget.value)}
            maxLength={2000}
            autosize
            minRows={2}
          />
          {item.kind === "reply" && (
            <Text size="xs" c="dimmed">
              {t("discardHandsOff")}
            </Text>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setDiscarding(false)}>
              {t("cancel")}
            </Button>
            <Button color="red" loading={discard.isPending} onClick={() => discard.mutate()}>
              {t("discard")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Card>
  );
};
