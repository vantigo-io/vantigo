import {
  ActionIcon,
  Button,
  Card,
  Checkbox,
  Group,
  Input,
  Modal,
  SegmentedControl,
  Select,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconTrash, IconWand } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useRef, useState } from "react";
import { type Claim, type PerDiemSuggestedDay, perDiemSuggestion } from "../api/claims";
import { createExpense, deleteExpense, type Expense, updateExpense } from "../api/entries";
import { type ApiError, ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useExpenseFormat } from "../lib/format";
import { type Meal, mealLabelKey, meals, type PerDiem, perDiemTypeLabelKey, perDiemTypes } from "../lib/per-diem";

/** What a travel claim may hold, from the contract. */
export const CLAIM_LINE_CAP = 200;

export interface PerDiemSectionProps {
  claim: Claim;
  /** The trip's per diem lines, oldest day first — the claim's own ordering. */
  days: Expense[];
  /** How many expenses the trip holds altogether, against the 200-line cap. */
  lineCount: number;
  /** Refusals the submit answered, keyed by the line each one names. */
  refusals: Map<number, string[]>;
}

/**
 * The trip's per diem, day by day (design §4).
 *
 * Every figure comes from the server: the day rate, each meal's deduction and
 * the amount. A meal is saved the moment it is ticked, because the amount
 * beside it is the server's answer and a row that showed a tick without one
 * would be showing a figure nobody had worked out. Each row carries **the
 * revision its own last answer gave**, so two ticks in a row do not race each
 * other into a 409.
 */
export const PerDiemSection = ({ claim, days, lineCount, refusals }: PerDiemSectionProps) => {
  const { t } = useI18n("expenses");
  const [suggesting, setSuggesting] = useState(false);
  const editable = claim.capabilities.canEdit;
  const room = CLAIM_LINE_CAP - lineCount;

  return (
    <Card withBorder padding="lg" radius="md" data-testid="per-diem-section">
      <Stack gap="md">
        <Group justify="space-between" align="start" wrap="wrap">
          <Stack gap={0}>
            <Title order={4}>{t("perDiem")}</Title>
            <Text size="sm" c="dimmed">
              {t("perDiemDescription")}
            </Text>
          </Stack>
          {editable && (
            <Button
              variant="light"
              leftSection={<IconWand size={16} />}
              disabled={room <= 0}
              onClick={() => setSuggesting(true)}
            >
              {t("suggestDays")}
            </Button>
          )}
        </Group>

        {room <= 0 && (
          <Text size="sm" c="orange">
            {t("claimLineCapReached")}
          </Text>
        )}

        {days.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("perDiemNoneDescription")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={720}>
            <Table aria-label={t("perDiemDays")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("date")}</Table.Th>
                  <Table.Th>{t("perDiemType")}</Table.Th>
                  <Table.Th>{t("perDiemDayRate")}</Table.Th>
                  <Table.Th>{t("perDiem")}</Table.Th>
                  <Table.Th>{t("amount")}</Table.Th>
                  <Table.Th>{t("rowActions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {days.map((day) => (
                  <PerDiemDayRow
                    key={day.id}
                    claim={claim}
                    day={day}
                    editable={editable}
                    refusals={refusals.get(day.id) ?? []}
                  />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}

        <Text size="xs" c="dimmed">
          {t("perDiemLinesLeft", { count: lineCount })}
        </Text>
      </Stack>

      <SuggestDaysModal claim={claim} opened={suggesting} room={room} onClose={() => setSuggesting(false)} />
    </Card>
  );
};

const mealFlag = (perDiem: PerDiem, meal: Meal): boolean =>
  meal === "breakfast" ? perDiem.breakfastCovered : meal === "lunch" ? perDiem.lunchCovered : perDiem.dinnerCovered;

const PerDiemDayRow = ({
  claim,
  day,
  editable,
  refusals,
}: {
  claim: Claim;
  day: Expense;
  editable: boolean;
  refusals: string[];
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const queryClient = useQueryClient();
  /**
   * The line as this row knows it: the one the claim was read at, then
   * whatever a write here answered. The revision it carries is the one the
   * last answer gave — never a refetched one — so a second tick in the same
   * open row is guarded by exactly what the first save produced.
   */
  const [line, setLine] = useState(day);
  /**
   * Where a refusal lands. The server names the field it is about, so a
   * covered meal the table prices nothing for belongs under *that* checkbox,
   * a kind of day nothing prices under the select that chose it, and
   * everything else — a conflict above all — in the row's own message.
   */
  const [rowError, setRowError] = useState<string | undefined>(undefined);
  const [typeError, setTypeError] = useState<string | undefined>(undefined);
  const [mealErrors, setMealErrors] = useState<Partial<Record<Meal, string>>>({});

  // A refetch that brings a genuinely different line (the header save
  // repriced it) replaces what the row holds; a stale render does not.
  const [seen, setSeen] = useState(day);
  if (seen !== day) {
    setSeen(day);
    if (day.revision > line.revision) setLine(day);
  }

  const perDiem = line.perDiem;
  const label = format.date(line.entryDate);

  const save = useMutation({
    mutationFn: (next: { type: PerDiem["type"]; covered: Record<Meal, boolean> }) =>
      updateExpense(line.id, {
        kind: "per_diem",
        claimId: claim.id,
        entryDate: line.entryDate,
        perDiemType: next.type,
        breakfastCovered: next.covered.breakfast,
        lunchCovered: next.covered.lunch,
        dinnerCovered: next.covered.dinner,
        revision: line.revision,
      }),
    onSuccess: async (saved) => {
      setRowError(undefined);
      setTypeError(undefined);
      setMealErrors({});
      setLine(saved);
      setSeen(saved);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
    },
    onError: async (failure: Error) => {
      setRowError(undefined);
      setTypeError(undefined);
      setMealErrors({});
      if (failure instanceof ApiValidationError) {
        const fields = failure.fieldErrors;
        const onMeals: Partial<Record<Meal, string>> = {};
        for (const meal of meals) {
          const message = fields[`${meal}Covered`];
          if (message) onMeals[meal] = message;
        }
        const elsewhere = Object.entries(fields)
          .filter(([field]) => field !== "perDiemType" && !meals.some((meal) => `${meal}Covered` === field))
          .map(([, message]) => message);
        setMealErrors(onMeals);
        setTypeError(fields.perDiemType);
        setRowError(
          elsewhere[0] ?? (fields.perDiemType || Object.keys(onMeals).length > 0 ? undefined : failure.message),
        );
        return;
      }
      // A conflict is not about a field at all: somebody moved the trip while
      // the tick was in flight, so the row says so and the page reads it again.
      const conflict = (failure as ApiError).status === 409;
      setRowError(conflict ? t("claimChangedElsewhere") : failure.message);
      if (conflict) await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
    },
  });

  const remove = useMutation({
    mutationFn: () => deleteExpense(line.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("perDiemDayRemoved"), message: "" });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotRemovePerDiemDay"), message: refusalMessage(failure) }),
  });

  if (!perDiem) return null;

  const covered = Object.fromEntries(meals.map((meal) => [meal, mealFlag(perDiem, meal)])) as Record<Meal, boolean>;

  return (
    <Table.Tr data-per-diem-day={line.id}>
      <Table.Td>{label}</Table.Td>
      <Table.Td>
        {editable ? (
          <Select
            aria-label={t("perDiemTypeOf", { date: label })}
            w={200}
            allowDeselect={false}
            error={typeError}
            data={perDiemTypes.map((type) => ({ value: type, label: t(perDiemTypeLabelKey(type)) }))}
            value={perDiem.type}
            disabled={save.isPending}
            onChange={(next) => next && save.mutate({ type: next as PerDiem["type"], covered })}
          />
        ) : (
          <Text size="sm">{t(perDiemTypeLabelKey(perDiem.type))}</Text>
        )}
      </Table.Td>
      <Table.Td>{format.money(perDiem.dayRate, line.currency)}</Table.Td>
      <Table.Td>
        <Stack gap={2}>
          {meals.map((meal) => (
            <Checkbox
              key={meal}
              size="sm"
              aria-label={t("mealCoveredOn", { meal: t(mealLabelKey(meal)), date: label })}
              error={mealErrors[meal]}
              label={
                perDiem.mealPercents[meal] === undefined
                  ? t("mealNotPriced", { meal: t(mealLabelKey(meal)) })
                  : `${t(mealLabelKey(meal))} · ${format.number(perDiem.mealPercents[meal] ?? 0, 0)} %`
              }
              checked={covered[meal]}
              disabled={!editable || save.isPending}
              onChange={(event) =>
                save.mutate({ type: perDiem.type, covered: { ...covered, [meal]: event.currentTarget.checked } })
              }
            />
          ))}
        </Stack>
      </Table.Td>
      <Table.Td>
        <Stack gap={2}>
          <Text size="sm">{format.money(line.grossAmount, line.currency)}</Text>
          <RefusalList messages={[...refusals, ...(rowError ? [rowError] : [])]} />
        </Stack>
      </Table.Td>
      <Table.Td>
        {editable && (
          <ActionIcon
            variant="subtle"
            color="red"
            aria-label={t("removePerDiemDay", { date: label })}
            loading={remove.isPending}
            onClick={() =>
              modals.openConfirmModal({
                title: t("deleteExpenseTitle"),
                children: (
                  <Text size="sm">
                    {t("deleteExpenseConfirm", { description: t(perDiemTypeLabelKey(perDiem.type)) })}
                  </Text>
                ),
                labels: { confirm: t("delete"), cancel: t("cancel") },
                confirmProps: { color: "red" },
                onConfirm: () => remove.mutate(),
              })
            }
          >
            <IconTrash size={16} />
          </ActionIcon>
        )}
      </Table.Td>
    </Table.Tr>
  );
};

/**
 * "Suggest days": the one question the times cannot answer, then the days the
 * server proposes — priced, marked where the trip already holds one, and with
 * the reason beside a day no rate applies to.
 *
 * "Add" posts them one at a time and **stops at the first refusal**: a trip
 * holds at most 200 expenses and the claim may have changed underneath, so
 * what did land stays and the caller is told where it stopped.
 */
const SuggestDaysModal = ({
  claim,
  opened,
  room,
  onClose,
}: {
  claim: Claim;
  opened: boolean;
  room: number;
  onClose: () => void;
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const queryClient = useQueryClient();
  const [overnight, setOvernight] = useState("no");
  const [suggested, setSuggested] = useState<PerDiemSuggestedDay[] | undefined>(undefined);
  const [picked, setPicked] = useState<string[]>([]);
  const [refusals, setRefusals] = useState<string[]>([]);
  /** The day an add was on when it was refused, so the client can name it. */
  const failedOn = useRef<string | undefined>(undefined);

  const close = () => {
    setSuggested(undefined);
    setPicked([]);
    setRefusals([]);
    onClose();
  };

  /**
   * Asks again and re-picks. A day the trip already holds comes back
   * `exists: true` and is left unticked — what to do about it is the
   * traveller's decision, not the server's — and a day with no rate cannot be
   * added at all. Running it after a partial add is what stops a second press
   * re-posting a day that already landed.
   */
  const loadSuggestion = async () => {
    const days = await perDiemSuggestion(claim.id, overnight === "yes");
    setSuggested(days);
    setPicked(days.filter((day) => !day.exists && day.dayRate !== undefined).map((day) => day.entryDate));
  };

  const ask = useMutation({
    mutationFn: loadSuggestion,
    onSuccess: () => setRefusals([]),
    onError: (error: Error) => setRefusals([refusalMessage(error)]),
  });

  const addable = (suggested ?? []).filter((day) => picked.includes(day.entryDate));
  const capped = addable.slice(0, Math.max(0, room));

  const add = useMutation({
    mutationFn: async () => {
      let added = 0;
      for (const day of capped) {
        failedOn.current = day.entryDate;
        await createExpense({
          kind: "per_diem",
          claimId: claim.id,
          entryDate: day.entryDate,
          perDiemType: day.perDiemType,
        });
        failedOn.current = undefined;
        added += 1;
      }
      return added;
    },
    onSuccess: async (added) => {
      setRefusals([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({
        color: "teal",
        title: t("perDiemDaysAdded"),
        message: added === 1 ? t("oneExpense") : t("countOfExpenses", { count: added }),
      });
      close();
    },
    onError: async (error: Error) => {
      // Whatever landed before the refusal is recorded, so the trip and the
      // suggestion are both read again: the days that got in come back marked
      // and unticked, and pressing "Add" again retries only what is left.
      const stoppedOn = failedOn.current;
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      await loadSuggestion().catch(() => undefined);
      // The server's sentence does not always name a day — the cap and the
      // "read it again" refusal name none at all — so the client says which.
      setRefusals([
        ...(stoppedOn ? [t("perDiemDayFailed", { date: format.date(stoppedOn) })] : []),
        refusalMessage(error, "claimId"),
      ]);
    },
  });

  return (
    <Modal opened={opened} onClose={close} title={t("suggestDaysTitle")} centered size="lg">
      <Stack>
        <RefusalList messages={refusals} />
        <Input.Wrapper label={t("overnightQuestion")} description={t("overnightDescription")} labelElement="div">
          <SegmentedControl
            mt={4}
            aria-label={t("overnightQuestion")}
            value={overnight}
            onChange={(next) => {
              // The preview belongs to the answer that produced it. Keeping it
              // after a flip would offer "No" days under a "Yes", and the
              // after-failure reload would ask with the other answer.
              setOvernight(next);
              setSuggested(undefined);
              setPicked([]);
              setRefusals([]);
            }}
            data={[
              { value: "no", label: t("no") },
              { value: "yes", label: t("yes") },
            ]}
          />
        </Input.Wrapper>

        <Group>
          <Button loading={ask.isPending} onClick={() => ask.mutate()}>
            {t("suggestDaysAction")}
          </Button>
        </Group>

        {suggested && suggested.length === 0 && <Text size="sm">{t("suggestedNothing")}</Text>}

        {suggested && suggested.length > 0 && (
          <Table.ScrollContainer minWidth={480}>
            <Table aria-label={t("suggestedDaysHeading")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("select")}</Table.Th>
                  <Table.Th>{t("date")}</Table.Th>
                  <Table.Th>{t("perDiemType")}</Table.Th>
                  <Table.Th>{t("amount")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {suggested.map((day) => (
                  <Table.Tr key={day.entryDate}>
                    <Table.Td>
                      <Checkbox
                        aria-label={t("selectSuggestedDay", { date: format.date(day.entryDate) })}
                        checked={picked.includes(day.entryDate)}
                        disabled={day.dayRate === undefined}
                        onChange={(event) =>
                          setPicked((current) =>
                            event.currentTarget.checked
                              ? [...current, day.entryDate]
                              : current.filter((date) => date !== day.entryDate),
                          )
                        }
                      />
                    </Table.Td>
                    <Table.Td>{format.date(day.entryDate)}</Table.Td>
                    <Table.Td>
                      <Stack gap={0}>
                        <Text size="sm">{t(perDiemTypeLabelKey(day.perDiemType))}</Text>
                        {day.exists && (
                          <Text size="xs" c="dimmed">
                            {t("suggestedDayExists")}
                          </Text>
                        )}
                        {day.dayRate === undefined && (
                          <Text size="xs" c="orange">
                            {t("suggestedDayNoRate")}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      {/* The currency is the day's own — the server sends it
                          with the figures, absent with them — so nothing here
                          infers it from the claim. */}
                      {day.amount === undefined ? t("notAvailable") : format.money(day.amount, day.currency ?? "")}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}

        {addable.length > capped.length && (
          <Text size="sm" c="orange">
            {capped.length === 1 ? t("perDiemCapLimitsOne") : t("perDiemCapLimits", { count: capped.length })}
          </Text>
        )}

        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={close}>
            {t("cancel")}
          </Button>
          <Button disabled={capped.length === 0} loading={add.isPending} onClick={() => add.mutate()}>
            {capped.length === 1 ? t("addOneSuggestedDay") : t("addSuggestedDays", { count: capped.length })}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
