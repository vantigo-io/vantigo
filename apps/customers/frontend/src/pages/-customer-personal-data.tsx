import { Alert, Button, Group, Menu, Modal, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconCalendarX, IconChevronDown, IconDownload, IconShieldLock, IconUserOff } from "@tabler/icons-react";
import { type QueryClient, useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import {
  ApiConflictError,
  ApiValidationError,
  type CustomerResponse,
  customerQueryOptions,
  invalidateCustomersExcept,
  syncCustomerRevision,
} from "../api/customers";
import { isSessionExpired, saveCsv } from "../api/import-export";
import { cancelAnonymisation, downloadPersonalData, scheduleAnonymisation } from "../api/personal-data";
import { CUSTOMER_ANONYMISED_CODE, CUSTOMER_MERGED_CODE, customerWriteErrorMessage } from "../lib/customer-write-error";
import { utcToday } from "../lib/follow-up-dates";
import { formatDateOnly } from "../lib/format-date-only";
import "../i18n";

/** The server's scheduling refusals by code (GDPR design D4); anything else shows the server's own detail. */
const scheduleRefusalKeys: Record<string, string> = {
  personal_data_customer_active: "anonymisationRefusedActive",
  personal_data_not_a_person: "anonymisationRefusedNotAPerson",
  [CUSTOMER_ANONYMISED_CODE]: "customerAnonymisedMessage",
  [CUSTOMER_MERGED_CODE]: "customerMergedMessage",
};

/**
 * Both writes answer the customer as GET would give it, so it goes straight
 * into the cache — the banner changes without waiting for a refetch — and its
 * revision to every entry holding one; the rest of ["customers"] (the
 * timeline's new entry, the lists) is invalidated. The merge modal's shape.
 */
const storeAnswer = (queryClient: QueryClient, updated: CustomerResponse) => {
  const customerKey = customerQueryOptions(updated.id).queryKey;
  queryClient.setQueryData(customerKey, updated);
  syncCustomerRevision(queryClient, updated.id, updated.revision);
  invalidateCustomersExcept(queryClient, customerKey);
};

/**
 * Personal data (customers GDPR design D5): a private person's export and the
 * scheduling of their anonymisation, behind the host's `canManagePersonalData`
 * (`customers:personal-data`) — the header renders it for a person only, since
 * a business is not a data subject. Export stays after the anonymisation (it
 * answers what is left); scheduling goes then, with nothing left to schedule.
 * Scheduling needs an archived customer, and a merged-away one is scheduled on
 * its survivor: the item is there but off, with the reason under it, so a
 * person knows what to do rather than wondering where it went. Once scheduled,
 * the day can be moved or the schedule called off — through the shared confirm
 * modal, as every other write that undoes something here does.
 */
export const CustomerPersonalDataMenu = ({ customer }: { customer: CustomerResponse }) => {
  const { t, formatters } = useI18n("customers");
  const queryClient = useQueryClient();
  const [scheduling, setScheduling] = useState(false);
  const anonymisation = customer.anonymisation;
  const anonymised = Boolean(anonymisation?.anonymisedAt);
  const scheduledOn = anonymisation && !anonymised ? anonymisation.anonymiseOn : null;
  const blocked = customer.mergedInto
    ? t("anonymisationMergedAway")
    : customer.status !== "archived"
      ? t("anonymisationNeedsArchive")
      : null;

  const exportFile = useMutation({
    mutationFn: () => downloadPersonalData(customer),
    onSuccess: (file) => saveCsv(file),
    onError: (error) => {
      // An expired session has already been handed to the host, which signs
      // the person out; there is nothing left to show.
      if (isSessionExpired(error)) return;
      notifications.show({ color: "red", title: t("personalDataExportFailed"), message: error.message });
    },
  });
  const cancel = useMutation({
    mutationFn: () => cancelAnonymisation(customer.id),
    onSuccess: (updated) => {
      storeAnswer(queryClient, updated);
      notifications.show({
        color: "teal",
        title: t("anonymisationCancelledTitle"),
        message: t("anonymisationCancelledMessage", { name: customer.name }),
      });
    },
    onError: (error) => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      notifications.show({
        color: "red",
        title: t("anonymisationCouldNotBeSaved"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });
  const confirmCancel = (day: string) =>
    modals.openConfirmModal({
      title: t("anonymisationCancelTitle"),
      children: (
        <Text size="sm">
          {t("anonymisationCancelConfirm", { name: customer.name, date: formatDateOnly(formatters, day) })}
        </Text>
      ),
      labels: { confirm: t("anonymisationCancel"), cancel: t("anonymisationKeep") },
      onConfirm: () => cancel.mutate(),
    });

  return (
    <>
      <Menu position="bottom-end" withinPortal>
        <Menu.Target>
          <Button
            variant="subtle"
            color="gray"
            leftSection={<IconShieldLock size={16} />}
            rightSection={<IconChevronDown size={14} />}
            loading={exportFile.isPending}
          >
            {t("personalDataMenu")}
          </Button>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item leftSection={<IconDownload size={15} />} onClick={() => exportFile.mutate()}>
            {t("personalDataExport")}
          </Menu.Item>
          {!anonymised && (
            <>
              <Menu.Divider />
              <Menu.Item
                leftSection={<IconUserOff size={15} />}
                disabled={Boolean(blocked)}
                onClick={() => setScheduling(true)}
              >
                {scheduledOn ? t("anonymisationReschedule") : t("anonymisationSchedule")}
              </Menu.Item>
              {blocked && (
                <Text size="xs" c="dimmed" px="sm" pb={4} maw={280}>
                  {blocked}
                </Text>
              )}
              {scheduledOn && (
                <Menu.Item
                  leftSection={<IconCalendarX size={15} />}
                  color="red"
                  onClick={() => confirmCancel(scheduledOn)}
                >
                  {t("anonymisationCancel")}
                </Menu.Item>
              )}
            </>
          )}
        </Menu.Dropdown>
      </Menu>
      <AnonymisationScheduleModal customer={customer} opened={scheduling} onClose={() => setScheduling(false)} />
    </>
  );
};

/**
 * The day, chosen (GDPR design D4): no default — the modal says why, and says
 * that nothing brings the data back — and nothing before today in UTC, the
 * calendar the server counts the day in. A refusal the page could not have
 * known (archived no more, anonymised meanwhile, merged away from another tab)
 * is said in the modal in words, and the page refetches.
 */
const AnonymisationScheduleModal = ({
  customer,
  opened,
  onClose,
}: {
  customer: CustomerResponse;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t, formatters } = useI18n("customers");
  const queryClient = useQueryClient();
  const [refusal, setRefusal] = useState<string | null>(null);
  // A refusal left from the last time the modal was open is cleared on the
  // opening itself, adjusted during render rather than in an effect — the form
  // modal's seenState pattern (-customer-form-modal.tsx).
  const [seenOpened, setSeenOpened] = useState(opened);
  if (opened !== seenOpened) {
    setSeenOpened(opened);
    if (opened) setRefusal(null);
  }
  const form = useForm({
    initialValues: { anonymiseOn: "" },
    validate: {
      anonymiseOn: (value: string) =>
        !value ? t("anonymisationDateRequired") : value < utcToday() ? t("anonymisationDateInPast") : null,
    },
  });
  useEffect(() => {
    if (opened) {
      form.setValues({ anonymiseOn: customer.anonymisation?.anonymiseOn ?? "" });
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opened]);
  const mutation = useMutation({
    mutationFn: (anonymiseOn: string) => scheduleAnonymisation(customer.id, anonymiseOn),
    onSuccess: (updated, anonymiseOn) => {
      storeAnswer(queryClient, updated);
      notifications.show({
        color: "teal",
        title: t("anonymisationScheduledTitle"),
        message: t("anonymisationScheduledMessage", {
          name: customer.name,
          date: formatDateOnly(formatters, anonymiseOn),
        }),
      });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      if (error instanceof ApiConflictError && error.code && scheduleRefusalKeys[error.code]) {
        queryClient.invalidateQueries({ queryKey: ["customers"] });
        setRefusal(t(scheduleRefusalKeys[error.code]));
        return;
      }
      notifications.show({
        color: "red",
        title: t("anonymisationCouldNotBeSaved"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t("anonymisationModalTitle")}>
      <form onSubmit={form.onSubmit(({ anonymiseOn }) => mutation.mutate(anonymiseOn))}>
        <Stack gap="sm">
          <Text size="sm">{t("anonymisationModalIntro", { name: customer.name })}</Text>
          <Text size="sm">{t("anonymisationModalRetention")}</Text>
          <Text size="sm" fw={600}>
            {t("anonymisationModalIrreversible")}
          </Text>
          <DateInput
            label={t("anonymisationDate")}
            valueFormat="YYYY-MM-DD"
            minDate={utcToday()}
            withAsterisk
            {...form.getInputProps("anonymiseOn")}
          />
          {refusal && <Alert color="red">{refusal}</Alert>}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" color="red" loading={mutation.isPending}>
              {t("anonymisationConfirm")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
