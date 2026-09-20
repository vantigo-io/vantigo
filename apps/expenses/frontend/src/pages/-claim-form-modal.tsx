import {
  Alert,
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
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type Claim,
  type ClaimInput,
  type ClaimUpdateInput,
  createClaim,
  expenseClaimQueryOptions,
  updateClaim,
} from "../api/claims";
import { type ExpensesMeta, expensesMetaQueryOptions } from "../api/meta";
import { type ApiError, ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalMessage, refusalMessages } from "../lib/errors";
import { useDecimalSeparator } from "../lib/format";
import { useProjectOptions } from "../lib/project-options";
import { instantInZone, isKnownZone, isWallClockTime, wallClockInZone } from "../lib/time-zone";

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

/**
 * Nothing in the form is derived until the installation's own time zone is
 * known. `useForm`'s initial values are captured **once**, at mount, while
 * `payload()` converts with whatever zone is current by the time somebody
 * saves — so a form mounted against a placeholder zone captures one wall
 * clock and sends another, and the trip silently moves by the offset with no
 * refusal anywhere. Waiting one round trip is the whole fix.
 */
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
  const { data: meta } = useQuery(expensesMetaQueryOptions());
  if (!meta) return <ContentSkeleton rows={5} rowHeight={48} />;
  /**
   * A zone this browser cannot do arithmetic in is a refusal, not a fallback.
   * Reading a trip in the reader's own zone is cosmetic; **writing** one would
   * capture this device's offset under the installation's label and save the
   * trip hours off with no refusal at all, which is the one outcome nobody
   * would notice.
   */
  if (!isKnownZone(meta.timeZone)) {
    return (
      <Alert color="orange" icon={<IconAlertTriangle size={16} />} title={t("zoneUnknownHere")}>
        {t("zoneUnknownHereDescription", { zone: meta.timeZone })}
      </Alert>
    );
  }
  return <ClaimFormFields state={state} onClose={onClose} onSaved={onSaved} meta={meta} />;
};

const ClaimFormFields = ({
  state,
  onClose,
  onSaved,
  meta,
}: {
  state: ClaimModalState;
  onClose: () => void;
  onSaved?: (claim: Claim) => void;
  meta: ExpensesMeta;
}) => {
  const { t } = useI18n("expenses");
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();

  const opened = state.mode === "edit" ? state.claim : undefined;
  const [refusals, setRefusals] = useState<string[]>([]);
  /**
   * The revision the form is guarded by: the one it opened at, and after a
   * conflict the one the claim was **read again** at. A form that could only
   * ever 409 again is a trap; the typed values stay and the next save is
   * judged against the trip as it now stands.
   */
  const [revision, setRevision] = useState<number | undefined>(opened?.revision);

  const { options: projectOptions } = useProjectOptions(opened?.project, meta.projectsAvailable === true);

  const zone = meta.timeZone;
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

  const departureAt =
    values.departureDate && isWallClockTime(values.departureTime)
      ? instantInZone(values.departureDate, values.departureTime, zone)
      : undefined;
  const returnAt =
    values.returnDate && isWallClockTime(values.returnTime)
      ? instantInZone(values.returnDate, values.returnTime, zone)
      : undefined;

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
    ...(meta.projectsAvailable && values.projectId ? { projectId: Number(values.projectId) } : {}),
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
    onError: async (error: Error) => {
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
      if ((error as ApiError).status === 409 && opened) {
        // Read it again *here*, so the next "Save" carries the revision the
        // trip now stands at and what the traveller typed is still on screen.
        await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
        const fresh = await queryClient.fetchQuery(expenseClaimQueryOptions(opened.id)).catch(() => undefined);
        if (fresh) setRevision(fresh.revision);
        setRefusals([t("claimChangedElsewhere")]);
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveClaim"), message: refusalMessage(error) });
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

        {meta.projectsAvailable && (
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
