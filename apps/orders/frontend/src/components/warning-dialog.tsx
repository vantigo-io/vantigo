import { Button, Group, Modal, SegmentedControl, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ORDERS_QUERY_KEY } from "../api/request";
import type { Warning, WarningLevel, WarningScope } from "../api/types";
import { deleteWarning, putWarning } from "../api/warnings";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";

export interface WarningDialogProps {
  scope: WarningScope;
  /** The customer's or the product's id. */
  id: number;
  /** What the warning is about, as the dialog names it. */
  subject: string;
  /** The warning it has now, which the dialog edits or removes. */
  current?: Warning;
  onClose: () => void;
}

/**
 * Sets, changes or removes a customer's or a product's sales warning (O33),
 * under `orders:settings`: an info warning is shown on every quote and order
 * it applies to, a block also stops send and confirm until it is overridden.
 * Every Orders query is read again after, so the banners follow.
 */
export const WarningDialog = ({ scope, id, subject, current, onClose }: WarningDialogProps) => {
  const { t } = useOrdersFormat();
  const queryClient = useQueryClient();
  const [level, setLevel] = useState<WarningLevel>(current?.level ?? "info");
  const [text, setText] = useState(current?.text ?? "");
  const [error, setError] = useState<string>();
  const done = async (message: string) => {
    await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
    notifications.show({ color: "green", message });
    onClose();
  };
  const save = useMutation({
    mutationFn: () => putWarning(scope, id, { level, text: text.trim() }),
    onSuccess: () => done(t("warningSaved")),
    onError: (e) => setError(refusalMessage(e, t)),
  });
  const remove = useMutation({
    mutationFn: () => deleteWarning(scope, id),
    onSuccess: () => done(t("warningRemoved")),
    onError: (e) => setError(refusalMessage(e, t)),
  });
  return (
    <Modal opened onClose={onClose} title={t(scope === "customer" ? "customerWarning" : "productWarning")}>
      <Stack>
        <Text size="sm" fw={600}>
          {subject}
        </Text>
        <SegmentedControl
          aria-label={t("warningLevel")}
          value={level}
          onChange={(value) => setLevel(value as WarningLevel)}
          data={[
            { value: "info", label: t("warningLevel.info") },
            { value: "block", label: t("warningLevel.block") },
          ]}
        />
        <Text size="xs" c="dimmed">
          {level === "block" ? t("warningBlockHint") : t("warningInfoHint")}
        </Text>
        <Textarea
          label={t("warningText")}
          autosize
          minRows={2}
          maxLength={500}
          required
          value={text}
          error={error}
          onChange={(e) => setText(e.currentTarget.value)}
          data-autofocus
        />
        <Group justify="space-between">
          {current ? (
            <Button variant="subtle" color="red" loading={remove.isPending} onClick={() => remove.mutate()}>
              {t("removeWarning")}
            </Button>
          ) : (
            <span />
          )}
          <Group>
            <Button variant="default" onClick={onClose}>
              {t("cancelDialog")}
            </Button>
            <Button loading={save.isPending} disabled={text.trim() === ""} onClick={() => save.mutate()}>
              {t("saveWarning")}
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  );
};
