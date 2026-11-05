import { Box, Group, Text } from "@mantine/core";
import { IconCheck } from "@tabler/icons-react";
import { Fragment } from "react";

export interface LifecycleStep {
  key: string;
  label: string;
  /** done: passed; current: where the document is; ahead: not yet; off: a branch it did not take. */
  state: "done" | "current" | "ahead";
  /** A step that ended the document another way — declined, expired, cancelled — is told in red or orange. */
  tone?: "red" | "orange";
}

/**
 * The document's path as a thin track across the top of the editor: the
 * steps passed are filled, the one it is at is ringed, the ones ahead are
 * faint. It says at a glance where a quote or an order is in its life
 * (O18, O20) without reading the badges.
 */
export const Lifecycle = ({ steps, label }: { steps: readonly LifecycleStep[]; label: string }) => (
  <Group gap={0} wrap="nowrap" aria-label={label} role="list" data-testid="lifecycle" style={{ overflowX: "auto" }}>
    {steps.map((step, index) => {
      const colour = step.tone ?? "blue";
      const filled = step.state !== "ahead";
      return (
        <Fragment key={step.key}>
          {index > 0 && (
            <Box
              aria-hidden
              h={2}
              miw={24}
              style={{
                flex: 1,
                background: filled ? `var(--mantine-color-${colour}-filled)` : "var(--mantine-color-default-border)",
              }}
            />
          )}
          <Group gap={6} wrap="nowrap" role="listitem" aria-current={step.state === "current" ? "step" : undefined}>
            <Box
              aria-hidden
              w={18}
              h={18}
              style={{
                borderRadius: "50%",
                display: "grid",
                placeItems: "center",
                flexShrink: 0,
                background:
                  step.state === "done" ? `var(--mantine-color-${colour}-filled)` : "var(--mantine-color-body)",
                border: `2px solid ${filled ? `var(--mantine-color-${colour}-filled)` : "var(--mantine-color-default-border)"}`,
                boxShadow: step.state === "current" ? `0 0 0 3px var(--mantine-color-${colour}-light)` : undefined,
              }}
            >
              {step.state === "done" && <IconCheck size={11} color="white" stroke={3} />}
            </Box>
            <Text
              size="xs"
              fw={step.state === "current" ? 700 : 500}
              c={step.state === "ahead" ? "dimmed" : step.tone ? `${step.tone}.7` : undefined}
              tt="uppercase"
              style={{ letterSpacing: "0.04em", whiteSpace: "nowrap" }}
            >
              {step.label}
            </Text>
          </Group>
        </Fragment>
      );
    })}
  </Group>
);
