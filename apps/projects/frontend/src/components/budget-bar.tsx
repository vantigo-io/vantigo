import { Box, Group, Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { CSSProperties } from "react";
import "../i18n";
import {
  type BudgetBasis,
  type BudgetSegments,
  budgetBarGeometry,
  budgetUnit,
  segmentValue,
  useEconomyFormat,
} from "../lib/economy";

export interface BudgetBarProps {
  segments: BudgetSegments;
  /** Which budget the bar is drawn against; absent when there is none to draw against. */
  basis?: BudgetBasis;
  /** The budget the marker stands at, in the basis' own unit. */
  budget?: number | null;
  currency?: string;
  /** The server's word on whether the budget was passed — never re-derived from a rounded percentage. */
  overBudget: boolean;
  /** `sm` is the table-row variant: no legend, and the numbers live in the label alone. */
  size?: "sm" | "md";
}

type Bucket = "approved" | "submitted" | "draft";

const buckets: Bucket[] = ["approved", "submitted", "draft"];

const legendKeys: Record<Bucket, string> = {
  approved: "budgetSegmentApproved",
  submitted: "budgetSegmentSubmitted",
  draft: "budgetSegmentDraft",
};

/**
 * Approved is solid, submitted is the same colour lighter, and draft is
 * hatched: the three are told apart by pattern as well as by colour, so the
 * bar reads the same to somebody who cannot tell the two blues apart.
 */
const fills: Record<Bucket, CSSProperties> = {
  approved: { backgroundColor: "var(--mantine-color-blue-6)" },
  submitted: { backgroundColor: "var(--mantine-color-blue-3)" },
  draft: {
    backgroundColor: "var(--mantine-color-blue-1)",
    backgroundImage: "repeating-linear-gradient(45deg, var(--mantine-color-blue-4) 0 3px, transparent 3px 7px)",
  },
};

/** Percentages are written with the float noise trimmed off, so a width is "52.5%" and not "52.50000000000001%". */
const pct = (value: number) => `${Number(value.toFixed(4))}%`;

/**
 * What has been logged against what was planned: one stacked bar of the three
 * buckets, a marker where the budget stands, and the stretch past it in red.
 * The bar is scaled by `max(logged, budget)`, so the marker of an over-budget
 * project stays inside the bar rather than falling off its end, and a project
 * with no budget at all is simply the three buckets against their own total.
 *
 * Everything the bar shows is in the basis' own unit — a budget in money is
 * drawn in money — because a percentage measured against money and hours left
 * over against an hours budget are two different sentences, and the caller
 * names the basis beside the bar.
 */
export const BudgetBar = ({ segments, basis, budget, currency, overBudget, size = "md" }: BudgetBarProps) => {
  const { t } = useI18n("projects");
  const { hours, money, inUnit } = useEconomyFormat(currency);
  const unit = budgetUnit(basis);
  const geometry = budgetBarGeometry(segments, unit, budget);

  const written = {
    used: inUnit(unit, geometry.total),
    approved: inUnit(unit, segmentValue(segments.approved, unit)),
    submitted: inUnit(unit, segmentValue(segments.submitted, unit)),
    draft: inUnit(unit, segmentValue(segments.draft, unit)),
  };
  const label =
    geometry.marker === undefined
      ? t("budgetBarLabelNoBudget", written)
      : t(overBudget ? "budgetBarLabelOverBudget" : "budgetBarLabel", {
          ...written,
          budget: inUnit(unit, budget),
        });

  return (
    <Stack gap="xs">
      <Box
        role="img"
        aria-label={label}
        data-testid="budget-bar"
        style={{
          position: "relative",
          display: "flex",
          width: "100%",
          height: size === "sm" ? 8 : 18,
          borderRadius: "var(--mantine-radius-sm)",
          overflow: "hidden",
          backgroundColor: "var(--mantine-color-gray-2)",
        }}
      >
        {/* A bucket with nothing in it draws nothing: a zero-width sliver is a
            line somebody would read as a value. */}
        {buckets.map((bucket) =>
          geometry.widths[bucket] > 0 ? (
            <Box
              key={bucket}
              data-testid={`budget-bar-${bucket}`}
              style={{ width: pct(geometry.widths[bucket]), height: "100%", ...fills[bucket] }}
            />
          ) : null,
        )}
        {geometry.overflow && (
          <Box
            data-testid="budget-bar-overflow"
            style={{
              position: "absolute",
              top: 0,
              bottom: 0,
              left: pct(geometry.overflow.from),
              width: pct(geometry.overflow.to - geometry.overflow.from),
              backgroundColor: "var(--mantine-color-red-6)",
              opacity: 0.55,
            }}
          />
        )}
        {geometry.marker !== undefined && (
          <Box
            data-testid="budget-bar-marker"
            style={{
              position: "absolute",
              top: 0,
              bottom: 0,
              left: pct(geometry.marker),
              width: 2,
              transform: "translateX(-1px)",
              backgroundColor: "var(--mantine-color-dark-5)",
            }}
          />
        )}
      </Box>

      {size === "md" && (
        <Group gap="lg" wrap="wrap" data-testid="budget-bar-legend">
          {buckets.map((bucket) => {
            const amount = segments[bucket].amount;
            return (
              <Group key={bucket} gap={6} wrap="nowrap" data-testid={`budget-legend-${bucket}`}>
                <Box w={10} h={10} style={{ borderRadius: 2, ...fills[bucket] }} />
                <Text size="xs" c="dimmed">
                  {t(legendKeys[bucket])}
                </Text>
                <Text size="xs">{hours(segments[bucket].hours)}</Text>
                {amount !== undefined && amount !== null && (
                  <Text size="xs" c="dimmed">
                    {money(amount)}
                  </Text>
                )}
              </Group>
            );
          })}
        </Group>
      )}
    </Stack>
  );
};
