import { Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { type BudgetSegments, useEconomyFormat } from "../lib/economy";

export interface LoggedSplitProps {
  segments: BudgetSegments;
  /** The three buckets added up, as the server reports it. */
  totalHours: number;
}

/**
 * What has been logged, in words, for the places that draw no bar: a line or a
 * project with no budget to measure against. A bar there would be full width
 * whatever was logged, which reads as "all of it used" — but every economy
 * surface still owes the reader the three buckets (constraints, E2), so the
 * split is written out instead.
 *
 * Hours only: the surfaces that use this have a column of their own for what
 * the work is worth.
 */
export const LoggedSplit = ({ segments, totalHours }: LoggedSplitProps) => {
  const { t } = useI18n("projects");
  const { hours } = useEconomyFormat();

  // A bucket with nothing in it is left out rather than written as "0 h": the
  // line is read at a glance and three zeroes crowd out the one figure that
  // matters.
  const parts = [
    segments.approved.hours > 0 ? t("loggedApproved", { hours: hours(segments.approved.hours) }) : undefined,
    segments.submitted.hours > 0 ? t("loggedSubmitted", { hours: hours(segments.submitted.hours) }) : undefined,
    segments.draft.hours > 0 ? t("loggedDraft", { hours: hours(segments.draft.hours) }) : undefined,
  ].filter((part): part is string => part !== undefined);

  return (
    <Stack gap={0} data-testid="logged-split">
      <Text size="sm">{hours(totalHours)}</Text>
      {parts.length > 0 && (
        <Text size="xs" c="dimmed">
          {parts.join(" · ")}
        </Text>
      )}
    </Stack>
  );
};
