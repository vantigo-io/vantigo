import { ActionIcon, Anchor, Badge, Button, Group, Paper, SimpleGrid, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconCamera, IconCheck, IconPlusMinus, IconX } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import { useRef, useState } from "react";
import {
  checkPhotoUrl,
  type ManufacturingCheckResultInput,
  type ManufacturingWorkOrderCheck,
  orderedChecks,
  recordCheckResult,
  uploadCheckPhoto,
} from "../api/quality";
import { isMeasure } from "../lib/bom-body";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { resizePhoto } from "../lib/photo";
import { toggleSign } from "../lib/quality";
import { deviationLinkOptions } from "../lib/routes";
import { useAtStation } from "../lib/station";
import { Markdown } from "./markdown";

/** The station's buttons: well past a 48 px touch target (M101). */
const TOUCH_HEIGHT = 64;

export interface ChecksPanelProps {
  workOrderId: number;
  checks: readonly ManufacturingWorkOrderCheck[];
  /** Whether the caller may record: shop-floor or orders-manage, on a work order not done or cancelled. */
  canRecord: boolean;
}

const resultColor = { pass: "teal", done: "teal", fail: "red" } as const;

/** One check: what it asks, its result in force, and the large buttons that record one. */
const CheckRow = ({
  workOrderId,
  check,
  canRecord,
}: {
  workOrderId: number;
  check: ManufacturingWorkOrderCheck;
  canRecord: boolean;
}) => {
  const { t, decimal, stamp } = useManufacturingFormat();
  const atStation = useAtStation();
  const invalidate = useInvalidateManufacturing();
  const [busy, setBusy] = useState(false);
  const [value, setValue] = useState("");
  const [note, setNote] = useState("");
  const camera = useRef<HTMLInputElement>(null);
  const result = check.result;
  const limits = [check.min, check.max].some((limit) => limit !== null);

  const record = async (input: ManufacturingCheckResultInput) => {
    setBusy(true);
    try {
      const view = await recordCheckResult(workOrderId, check.id, {
        ...input,
        ...(note.trim() === "" ? {} : { note: note.trim() }),
      });
      const recorded = view.workOrder.checks?.find((one) => one.id === check.id)?.result;
      setValue("");
      setNote("");
      if (recorded?.result === "fail")
        notifications.show({
          color: "red",
          title: t("checkFailed"),
          message: recorded.deviationNumber ? t("deviationOpened", { number: recorded.deviationNumber }) : "",
        });
      else notifications.show({ color: "teal", title: t("checkRecorded"), message: check.title });
    } catch (error) {
      notifications.show({ color: "red", title: t("couldNotDoThat"), message: refusalMessage(t, error as Error) });
    } finally {
      setBusy(false);
      await invalidate();
    }
  };

  const photographed = async (file: File | undefined) => {
    if (!file) return;
    setBusy(true);
    try {
      // Drawn down to 2560 px first: a camera's picture is several megabytes.
      const photo = await uploadCheckPhoto(workOrderId, await resizePhoto(file));
      await record({ photoId: photo.id });
    } catch (error) {
      notifications.show({ color: "red", title: t("couldNotDoThat"), message: refusalMessage(t, error as Error) });
      setBusy(false);
    } finally {
      if (camera.current) camera.current.value = "";
    }
  };

  return (
    <Paper withBorder p="md" data-check-id={check.id}>
      <Stack gap="sm">
        <Group justify="space-between" wrap="nowrap" align="flex-start">
          <Stack gap={2}>
            <Text size="lg" fw={600}>
              {check.title}
            </Text>
            <Group gap="xs">
              <Badge variant="light" size="md">
                {t(`checkKind_${check.kind}`)}
              </Badge>
              <Badge variant="light" size="md" color={check.frequency === "first_piece" ? "orange" : "gray"}>
                {t(`checkFrequency_${check.frequency}`)}
              </Badge>
              {check.quarantineOnFail && (
                <Badge variant="light" size="md" color="red">
                  {t("quarantineOnFail")}
                </Badge>
              )}
            </Group>
          </Stack>
          {result ? (
            <Badge size="xl" color={resultColor[result.result]}>
              {t(`checkResult_${result.result}`)}
            </Badge>
          ) : (
            <Badge size="xl" variant="outline" color="gray">
              {t("checkNoResult")}
            </Badge>
          )}
        </Group>
        {check.instructions.trim() !== "" && <Markdown>{check.instructions}</Markdown>}
        {check.kind === "measure" && limits && (
          <Text size="md">
            {t("checkLimits", {
              min: check.min === null ? "…" : decimal(check.min),
              max: check.max === null ? "…" : decimal(check.max),
              unit: check.unit ?? "",
            })}
            {check.nominal !== null && ` · ${t("checkNominal", { nominal: decimal(check.nominal) })}`}
          </Text>
        )}
        {result && (
          <Stack gap={2}>
            <Text size="md">
              {result.value !== null && `${decimal(result.value)} ${check.unit ?? ""} · `}
              {t("recordedBy", { name: result.recordedBy.displayName, when: stamp(result.recordedAt) })}
            </Text>
            {result.note && (
              <Text size="md" c="dimmed">
                {result.note}
              </Text>
            )}
            {result.photoId !== null && !atStation && (
              <Anchor href={checkPhotoUrl(workOrderId, result.photoId)} target="_blank" rel="noreferrer" size="md">
                {t("viewPhoto")}
              </Anchor>
            )}
            {result.deviationId !== null && atStation && <Text size="md">{result.deviationNumber}</Text>}
            {result.deviationId !== null && !atStation && (
              <Anchor
                size="md"
                renderRoot={(props) => <Link {...deviationLinkOptions(result.deviationId ?? 0)} {...props} />}
              >
                {result.deviationNumber}
              </Anchor>
            )}
          </Stack>
        )}
        {canRecord && (
          <Stack gap="sm">
            {check.kind === "measure" && (
              <TextInput
                size="xl"
                inputMode="decimal"
                label={t("checkValue", { unit: check.unit ?? "" })}
                value={value}
                onChange={(event) => setValue(event.currentTarget.value)}
                error={value.trim() !== "" && !isMeasure(value) ? t("problem_measure") : undefined}
                rightSectionWidth={64}
                rightSection={
                  // A tablet's decimal keypad has no minus: the sign is a button.
                  <ActionIcon
                    size={48}
                    variant="light"
                    aria-label={t("checkToggleSign")}
                    onClick={() => setValue(toggleSign)}
                  >
                    <IconPlusMinus size={24} />
                  </ActionIcon>
                }
              />
            )}
            <TextInput
              size="md"
              label={t("checkNote")}
              maxLength={1000}
              value={note}
              onChange={(event) => setNote(event.currentTarget.value)}
            />
            <SimpleGrid cols={{ base: 1, xs: 2 }} spacing="md">
              {check.kind === "instruction" && (
                <Button
                  h={TOUCH_HEIGHT}
                  size="xl"
                  color="teal"
                  leftSection={<IconCheck size={28} />}
                  loading={busy}
                  onClick={() => void record({ result: "done" })}
                >
                  {t("checkDone")}
                </Button>
              )}
              {check.kind === "pass_fail" && (
                <>
                  <Button
                    h={TOUCH_HEIGHT}
                    size="xl"
                    color="teal"
                    leftSection={<IconCheck size={28} />}
                    loading={busy}
                    onClick={() => void record({ result: "pass" })}
                  >
                    {t("checkPass")}
                  </Button>
                  <Button
                    h={TOUCH_HEIGHT}
                    size="xl"
                    color="red"
                    leftSection={<IconX size={28} />}
                    loading={busy}
                    onClick={() => void record({ result: "fail" })}
                  >
                    {t("checkFail")}
                  </Button>
                </>
              )}
              {check.kind === "measure" && (
                <Button
                  h={TOUCH_HEIGHT}
                  size="xl"
                  leftSection={<IconCheck size={28} />}
                  loading={busy}
                  disabled={!isMeasure(value)}
                  onClick={() => void record({ value: value.trim().replace(",", ".") })}
                >
                  {t("checkRecord")}
                </Button>
              )}
              {check.kind === "photo" && (
                <>
                  <input
                    ref={camera}
                    type="file"
                    accept="image/jpeg,image/png"
                    capture="environment"
                    hidden
                    aria-label={t("takePhoto")}
                    onChange={(event) => void photographed(event.currentTarget.files?.[0])}
                  />
                  <Button
                    h={TOUCH_HEIGHT}
                    size="xl"
                    leftSection={<IconCamera size={28} />}
                    loading={busy}
                    onClick={() => camera.current?.click()}
                  >
                    {t("takePhoto")}
                  </Button>
                </>
              )}
            </SimpleGrid>
          </Stack>
        )}
      </Stack>
    </Paper>
  );
};

/**
 * The work order's Checks panel on the shop floor (manufacturing phase 2
 * M101): the first-piece checks without a result first, then the rest by
 * sequence; large Pass and Fail buttons, a numeric field for a measure with
 * its limits shown, Take photo through the tablet's camera. A result
 * recorded again supersedes the one in force; a fail opens a deviation.
 */
export const ChecksPanel = ({ workOrderId, checks, canRecord }: ChecksPanelProps) => {
  const { t } = useManufacturingFormat();
  if (checks.length === 0)
    return (
      <Text size="lg" c="dimmed">
        {t("noChecksHere")}
      </Text>
    );
  return (
    <Stack gap="md" data-assist="manufacturing.work-order.checks">
      {orderedChecks(checks).map((check) => (
        <CheckRow key={check.id} workOrderId={workOrderId} check={check} canRecord={canRecord} />
      ))}
    </Stack>
  );
};
