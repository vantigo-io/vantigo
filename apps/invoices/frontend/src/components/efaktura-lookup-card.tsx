import { Badge, Button, Group, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageSection } from "@vantigo/frontend-shell";
import { checkConsumerLookup, getConsumerLookup } from "../api/efaktura";
import { INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

/**
 * A person's eFaktura status on the customer page (invoices phase 6 design
 * I30): what the provider last answered, and Check eFaktura for an issuer.
 */
export const EfakturaLookupCard = ({ customerId, canCheck }: { customerId: number; canCheck: boolean }) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const key = [INVOICES_QUERY_KEY, "efaktura-lookup", customerId];
  const lookup = useQuery({ queryKey: key, queryFn: () => getConsumerLookup(customerId) });
  const check = useMutation({
    mutationFn: () => checkConsumerLookup(customerId),
    onSuccess: (fresh) => queryClient.setQueryData(key, fresh),
    onError: (error) =>
      notifications.show({ color: "red", title: t("efaktura.couldNotCheck"), message: refusalMessage(error, t, date) }),
  });
  if (!lookup.data) return null;
  const data = lookup.data;
  return (
    <PageSection mt="md">
      <Stack gap="xs" data-testid="efaktura-lookup">
        <Group justify="space-between">
          <Text fw={600} component="h3">
            {t("efaktura.lookupTitle")}
          </Text>
          <Badge color={data.sendable ? "green" : "gray"} variant="light">
            {t(`efaktura.lookup.${data.status}`)}
          </Badge>
        </Group>
        {data.checkedAt && <Text size="sm">{t("efaktura.checkedAt", { at: dateTime(data.checkedAt) })}</Text>}
        {canCheck && (
          <Group>
            <Button size="xs" variant="default" loading={check.isPending} onClick={() => check.mutate()}>
              {t("efaktura.check")}
            </Button>
          </Group>
        )}
      </Stack>
    </PageSection>
  );
};
