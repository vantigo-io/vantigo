import { Alert, Group, Loader, Slider, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { calibrationQueryOptions } from "../api/agents";
import type { RoleCalibration } from "../api/types";
import { type ChartSize, linePath, markerX, percent, pointAt } from "../lib/calibration";
import { clampThreshold, THRESHOLD_MAX, THRESHOLD_MIN } from "../lib/modes";

const size: ChartSize = { width: 420, height: 150, padding: 14 };

/** The two lines and the X marker (sketch 3): what would go out on its own, and how much of that a person had to fix. */
export const CalibrationChart = ({ calibration, x }: { calibration: RoleCalibration; x: number }) => {
  const { t } = useI18n("agents");
  const marker = markerX(x, size);
  return (
    <svg
      viewBox={`0 0 ${size.width} ${size.height}`}
      role="img"
      aria-label={t("calibrationChartLabel")}
      width="100%"
      style={{ maxWidth: size.width }}
    >
      <line
        x1={size.padding}
        y1={size.height - size.padding}
        x2={size.width - size.padding}
        y2={size.height - size.padding}
        stroke="var(--mantine-color-gray-4)"
      />
      <path
        d={linePath(calibration.points, (point) => point.autoShare, size)}
        fill="none"
        stroke="var(--mantine-color-blue-6)"
        strokeWidth={2}
      />
      <path
        d={linePath(calibration.points, (point) => point.badShare, size)}
        fill="none"
        stroke="var(--mantine-color-red-6)"
        strokeWidth={2}
      />
      <line
        x1={marker}
        y1={size.padding}
        x2={marker}
        y2={size.height - size.padding}
        stroke="var(--mantine-color-dark-4)"
        strokeDasharray="4 3"
        data-testid="calibration-marker"
      />
    </svg>
  );
};

/**
 * Calibration (V21): over the role's last 200 labelled replies, for the chosen
 * threshold, how much would have gone out on its own and how much of that a
 * person edited beyond 20 % or discarded. The figures come from the server; the
 * slider only reads them. Under 50 labelled replies the panel says so.
 */
export const CalibrationPanel = ({ roleId, threshold }: { roleId: string; threshold: number }) => {
  const { t } = useI18n("agents");
  const query = useQuery(calibrationQueryOptions(roleId));
  const [chosen, setChosen] = useState(clampThreshold(threshold));

  if (query.isPending) return <Loader size="sm" />;
  if (query.isError || !query.data)
    return (
      <Alert color="red" data-testid="calibration">
        {t("calibrationLoadFailed")}
      </Alert>
    );
  const calibration = query.data;
  if (calibration.notEnoughHistory)
    return (
      <Alert color="gray" title={t("calibrationTitle")} data-testid="calibration">
        {t("notEnoughHistory", { count: calibration.labelled, minimum: calibration.minimum })}
      </Alert>
    );
  const point = pointAt(calibration, chosen);
  return (
    <Stack gap="xs" data-testid="calibration">
      <Text size="sm" fw={500}>
        {t("calibrationTitle")}
      </Text>
      <Text size="xs" c="dimmed">
        {t("calibrationHelp", { count: calibration.labelled })}
      </Text>
      <Text size="sm" id={`calibration-x-${roleId}`}>
        {t("calibrationThreshold", { threshold: chosen })}
      </Text>
      <Slider
        aria-labelledby={`calibration-x-${roleId}`}
        min={THRESHOLD_MIN}
        max={THRESHOLD_MAX}
        step={1}
        value={chosen}
        onChange={setChosen}
      />
      {point && (
        <Stack gap={2}>
          <Group gap={6}>
            <span
              style={{ width: 10, height: 10, background: "var(--mantine-color-blue-6)", borderRadius: 2 }}
              aria-hidden
            />
            <Text size="sm" data-testid="calibration-auto">
              {t("calibrationAuto", { count: point.autoCount, share: percent(point.autoShare) })}
            </Text>
          </Group>
          <Group gap={6}>
            <span
              style={{ width: 10, height: 10, background: "var(--mantine-color-red-6)", borderRadius: 2 }}
              aria-hidden
            />
            <Text size="sm" data-testid="calibration-bad">
              {t("calibrationBad", { count: point.badCount, share: percent(point.badShare) })}
            </Text>
          </Group>
          <Text size="sm" data-testid="calibration-held">
            {t("calibrationHeld", { count: calibration.heldCount })}
          </Text>
        </Stack>
      )}
      <CalibrationChart calibration={calibration} x={chosen} />
    </Stack>
  );
};
