import { Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { useState } from "react";
import { useOrdersFormat } from "../lib/format";

export interface ReasonDialogProps {
  title: string;
  description?: string;
  label: string;
  submitLabel: string;
  /** Whether a reason must be given; the API takes an empty one for a decline and a hold. */
  required?: boolean;
  maxLength: number;
  danger?: boolean;
  loading?: boolean;
  onSubmit: (reason: string) => void;
  onClose: () => void;
}

/** A state change with words for why: Decline (O20), Hold (O18). The page owns the request. */
export const ReasonDialog = ({
  title,
  description,
  label,
  submitLabel,
  required = false,
  maxLength,
  danger = false,
  loading = false,
  onSubmit,
  onClose,
}: ReasonDialogProps) => {
  const { t } = useOrdersFormat();
  const [reason, setReason] = useState("");
  return (
    <Modal opened onClose={onClose} title={title}>
      <Stack>
        {description && <Text size="sm">{description}</Text>}
        <Textarea
          label={label}
          autosize
          minRows={2}
          required={required}
          maxLength={maxLength}
          value={reason}
          onChange={(e) => setReason(e.currentTarget.value)}
          data-autofocus
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancelDialog")}
          </Button>
          <Button
            color={danger ? "red" : undefined}
            loading={loading}
            disabled={required && reason.trim() === ""}
            onClick={() => onSubmit(reason.trim())}
          >
            {submitLabel}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
