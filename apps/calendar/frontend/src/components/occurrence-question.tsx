import { Button, Modal, Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { JSX } from "react";
import "../i18n";

export type OccurrenceAnswer = "occurrence" | "series";

export interface OccurrenceQuestionProps {
  opened: boolean;
  /** What the person is about to do to one occurrence of a series. */
  action: "save" | "delete";
  onAnswer: (answer: OccurrenceAnswer) => void;
  onCancel: () => void;
}

/**
 * The question a change or a delete of one occurrence of a series asks
 * first (design D8): this occurrence, or the series. Changing the series
 * drops the changes made to single occurrences (D4), and the button says so.
 */
export function OccurrenceQuestion({ opened, action, onAnswer, onCancel }: OccurrenceQuestionProps): JSX.Element {
  const { t } = useI18n("calendar");
  return (
    <Modal
      opened={opened}
      onClose={onCancel}
      title={action === "save" ? t("occurrenceQuestionSave") : t("occurrenceQuestionDelete")}
      centered
      zIndex={400}
    >
      <Stack gap="sm">
        <Button variant="light" onClick={() => onAnswer("occurrence")}>
          {t("thisOccurrence")}
        </Button>
        <Text size="xs" c="dimmed">
          {action === "save" ? t("thisOccurrenceSaveHint") : t("thisOccurrenceDeleteHint")}
        </Text>
        <Button variant="light" color={action === "delete" ? "red" : undefined} onClick={() => onAnswer("series")}>
          {t("theSeries")}
        </Button>
        <Text size="xs" c="dimmed">
          {action === "save" ? t("theSeriesSaveHint") : t("theSeriesDeleteHint")}
        </Text>
        <Button variant="subtle" color="gray" onClick={onCancel}>
          {t("cancel")}
        </Button>
      </Stack>
    </Modal>
  );
}
