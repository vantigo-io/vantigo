import {
  Button,
  Divider,
  Group,
  Input,
  Modal,
  NumberInput,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { DateInput, TimeInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type Claim, type ClaimInput, type ClaimUpdateInput, createClaim, updateClaim } from "../api/claims";
import { expensesMetaQueryOptions } from "../api/meta";
import { expenseProjectsQueryOptions } from "../api/projects";
import { type ApiError, ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalMessage, refusalMessages } from "../lib/errors";
import { useDecimalSeparator } from "../lib/format";
import { suggestedDayCount } from "../lib/per-diem";
import { instantInZone, isWallClockTime, wallClockInZone } from "../lib/time-zone";

/** What the contract allows on a travel claim's header. */
export const PURPOSE_MAX_LENGTH = 200;
export const DESTINATION_MAX_LENGTH = 200;

/** Recording a trip, or changing the header of one the caller opened. */
export type ClaimModalState = { mode: "create" } | { mode: "edit"; claim: Claim };

export interface ClaimFormModalProps {
  state: ClaimModalState | null;
  onClose: () => void;
  /**
   * What to do with the claim a save answered — the create modal navigates to
   * the trip it just made, and the claim page reads itself again so the days
   * the server repriced are the ones on screen.
   */
  onSaved?: (claim: Claim) => void;
}

interface ClaimFormValues {
  purpose: string;
  destination: string;
  abroad: string;
  abroadDayRate: number | string;
  abroadCurrency: string;
  departureDate: string | null;
  departureTime: string;
  returnDate: string | null;
  returnTime: string;
  projectId: string | null;
}

/** The request fields a refusal may name that this form has an input for. */
const formFields = new Set([
  "purpose",
  "destination",
  "abroad",
  "abroadDayRate",
  "abroadCurrency",
  "departureAt",
  "returnAt",
  "projectId",
]);

const numeric = (value: number | string): number | undefined => {
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined;
  const trimmed = value.trim().replace(",", ".");
  if (trimmed === "") return undefined;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : undefined;
};

/**
 * A trip's header. The traveller types a wall-clock day and time — "we left at
 * 07:00" — and what goes on the wire is the instant that clock names in the
 * **installation's** time zone, which `/meta` answers. The browser's own zone
 * is never it, and the field says out loud which zone the times are in.
 */
export const ClaimFormModal = ({ state, onClose, onSaved }: ClaimFormModalProps) => {
  const { t } = useI18n("expenses");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editTravelClaimTitle") : t("newTravelClaimTitle")}
      centered
      size="lg"
    >
      {state && (
        <ClaimForm
          key={state.mode === "edit" ? state.claim.id : "create"}
          state={state}
          onClose={onClose}
          onSaved={onSaved}
        />
      )}
    </Modal>
  );
};

const ClaimForm = ({
  state,
  onClose,
  onSaved,
}: {
  state: ClaimModalState;
  onClose: () => void;
  onSaved?: (claim: Claim) => void;
}) => {
  const { t } = useI18n("expenses");
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();

  const opened = state.mode === "edit" ? state.claim : undefined;
  const [refusals, setRefusals] = useState<string[]>([]);
  /**
   * The revision the form was opened at — never a refetched one. A save
   * answers with the claim as it now stands, and that answer is the only
   * thing that moves it on.
   */
  const [revision] = useState<number | undefined>(opened?.revision);

  const { data: meta } = useQuery(expensesMetaQueryOptions());
  const { data: projects } = useQuery({
    ...expenseProjectsQueryOptions(),
    enabled: meta?.projectsAvailable === true,
  });

  const zone = meta?.timeZone ?? "UTC";
  const departure = opened ? wallClockInZone(opened.departureAt, zone) : undefined;
  const returns = opened ? wallClockInZone(opened.returnAt, zone) : undefined;

  const form = useForm<ClaimFormValues>({
    initialValues: {
      purpose: opened?.purpose ?? "",
      destination: opened?.destination ?? "",
      abroad: opened?.abroad ? "abroad" : "domestic",
      abroadDayRate: opened?.abroadDayRate ?? "",
      abroadCurrency: opened?.abroadCurrency ?? "",
      departureDate: departure?.date ?? null,
      departureTime: departure?.time ?? "08:00",
      returnDate: returns?.date ?? null,
      returnTime: returns?.time ?? "16:00",
      projectId: opened?.project ? String(opened.project.id) : null,
    },
    validate: {
      purpose: (value) => {
        const purpose = value.trim();
        if (!purpose) return t("claimPurposeRequired");
        return purpose.length > PURPOSE_MAX_LENGTH ? t("claimPurposeTooLong") : null;
      },
      destination: (value) => (value.trim().length > DESTINATION_MAX_LENGTH ? t("claimDestinationTooLong") : null),
      departureDate: (value) => (value ? null : t("claimDepartureRequired")),
      departureTime: (value) => (isWallClockTime(value) ? null : t("claimDepartureRequired")),
      returnTime: (value) => (isWallClockTime(value) ? null : t("claimReturnRequired")),
      returnDate: (value, values) => {
        if (!value) return t("claimReturnRequired");
        if (!values.departureDate || !isWallClockTime(values.departureTime) || !isWallClockTime(values.returnTime)) {
          return null;
        }
        const from = `${values.departureDate}T${values.departureTime}`;
        const to = `${value}T${values.returnTime}`;
        return to > from ? null : t("claimReturnAfterDeparture");
      },
      abroadDayRate: (value, values) => {
        if (values.abroad !== "abroad") return null;
        const rate = numeric(value);
        return rate === undefined || rate <= 0 ? t("claimAbroadDayRateRequired") : null;
      },
      abroadCurrency: (value, values) =>
        values.abroad === "abroad" && value.trim().length !== 3 ? t("claimAbroadCurrencyRequired") : null,
    },
  });

  const values = form.values;
  const abroad = values.abroad === "abroad";

  /**
   * `GET /projects` lists only what the owner may book on *now*, while a save
   * grandfathers a link the claim already carries. Without the claim's own
   * project in the list the picker would render blank over "No project".
   */
  const keptProject =
    opened?.project && projects !== undefined && !projects.some((project) => project.id === opened.project?.id)
      ? opened.project
      : undefined;
  const projectOptions = [
    ...(projects ?? []).map((project) => ({ value: String(project.id), label: `${project.code} · ${project.name}` })),
    ...(keptProject
      ? [
          {
            value: String(keptProject.id),
            label: t("projectNoLongerBookable", { project: `${keptProject.code} · ${keptProject.name}` }),
          },
        ]
      : []),
  ];

  const departureAt =
    values.departureDate && isWallClockTime(values.departureTime)
      ? instantInZone(values.departureDate, values.departureTime, zone)
      : undefined;
  const returnAt =
    values.returnDate && isWallClockTime(values.returnTime)
      ? instantInZone(values.returnDate, values.returnTime, zone)
      : undefined;
  const dayCount = departureAt && returnAt ? suggestedDayCount(departureAt, returnAt, true) : 0;

  const payload = (): ClaimInput => ({
    purpose: values.purpose.trim(),
    ...(values.destination.trim() ? { destination: values.destination.trim() } : {}),
    abroad,
    ...(abroad
      ? {
          abroadDayRate: numeric(values.abroadDayRate) ?? 0,
          abroadCurrency: values.abroadCurrency.trim().toUpperCase(),
        }
      : {}),
    departureAt: departureAt ?? "",
    returnAt: returnAt ?? "",
    ...(meta?.projectsAvailable && values.projectId ? { projectId: Number(values.projectId) } : {}),
  });

  const save = useMutation({
    mutationFn: () => {
      const input = payload();
      return opened && revision !== undefined
        ? updateClaim(opened.id, { ...input, revision } as ClaimUpdateInput)
        : createClaim(input);
    },
    onSuccess: async (saved) => {
      setRefusals([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("claimSaved"), message: saved.purpose });
      onSaved?.(saved);
      onClose();
    },
    onError: (error: Error) => {
      if (error instanceof ApiValidationError) {
        const fields = Object.fromEntries(
          Object.entries(error.fieldErrors)
            .filter(([field]) => formFields.has(field))
            // The two instants are one pair of inputs each; a refusal about
            // the window names the end it is about, and the message belongs
            // on the day rather than nowhere.
            .map(([field, message]) => [
              field === "departureAt" ? "departureDate" : field === "returnAt" ? "returnDate" : field,
              message,
            ]),
        );
        if (Object.keys(fields).length > 0) {
          form.setErrors(fields);
          return;
        }
        setRefusals(refusalMessages(error));
        return;
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotSaveClaim"),
        message: conflict ? t("claimChangedElsewhere") : refusalMessage(error),
      });
    },
  });

  return (
    <form onSubmit={form.onSubmit(() => save.mutate())}>
      <Stack>
        <RefusalList messages={refusals} />

        <TextInput label={t("claimPurpose")} withAsterisk data-autofocus {...form.getInputProps("purpose")} />
        <TextInput label={t("claimDestination")} {...form.getInputProps("destination")} />

        <Input.Wrapper label={t("claimWhere")} labelElement="div">
          <SegmentedControl
            fullWidth
            mt={4}
            aria-label={t("claimWhere")}
            value={values.abroad}
            onChange={(next) => form.setFieldValue("abroad", next)}
            data={[
              { value: "domestic", label: t("claimDomestic") },
              { value: "abroad", label: t("claimAbroad") },
            ]}
          />
        </Input.Wrapper>

        {abroad && (
          <Stack gap="xs">
            <Group grow align="start">
              <NumberInput
                label={t("claimAbroadDayRate")}
                withAsterisk
                min={0}
                decimalScale={2}
                decimalSeparator={decimalSeparator}
                {...form.getInputProps("abroadDayRate")}
              />
              <TextInput
                label={t("claimAbroadCurrency")}
                withAsterisk
                maxLength={3}
                {...form.getInputProps("abroadCurrency")}
                onChange={(event) => form.setFieldValue("abroadCurrency", event.currentTarget.value.toUpperCase())}
              />
            </Group>
            <Text size="xs" c="dimmed">
              {t("claimAbroadHint")}
            </Text>
          </Stack>
        )}

        <Divider />

        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
          <DateInput
            label={t("claimDepartureDate")}
            valueFormat={t("dateInputFormat")}
            withAsterisk
            {...form.getInputProps("departureDate")}
          />
          <TimeInput
            label={t("claimDepartureTime")}
            aria-label={t("claimDepartureTime")}
            withAsterisk
            {...form.getInputProps("departureTime")}
          />
          <DateInput
            label={t("claimReturnDate")}
            valueFormat={t("dateInputFormat")}
            withAsterisk
            {...form.getInputProps("returnDate")}
          />
          <TimeInput
            label={t("claimReturnTime")}
            aria-label={t("claimReturnTime")}
            withAsterisk
            {...form.getInputProps("returnTime")}
          />
        </SimpleGrid>
        <Text size="xs" c="dimmed">
          {t("timesAreIn", { zone })}
        </Text>
        {dayCount > 0 && (
          <Text size="xs" c="dimmed">
            {t("claimTripDays", { count: dayCount })}
          </Text>
        )}

        {meta?.projectsAvailable && (
          <>
            <Divider />
            {projectOptions.length === 0 && !values.projectId ? (
              <Text size="sm" c="dimmed">
                {t("noBookableProjects")}
              </Text>
            ) : (
              <Select
                label={t("project")}
                placeholder={t("chooseProject")}
                clearable
                searchable
                data={projectOptions}
                value={values.projectId}
                error={form.errors.projectId}
                onChange={(next) => form.setFieldValue("projectId", next)}
              />
            )}
          </>
        )}

        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
