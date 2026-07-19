"use client";

import { Badge, Modal } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconBuilding, IconUser } from "@tabler/icons-react";
import type { Customer } from "@vantigo/customers/database/schema/customers";
import { useCreateCustomer, useUpdateCustomer } from "@vantigo/customers/lib/customers/hooks";
import type { DuplicateWarning } from "@vantigo/customers/lib/customers/queries";
import type { CustomerCreateInput } from "@vantigo/customers/lib/customers/schemas";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { CustomerForm } from "./customer-form";

export function CustomerStatusBadge({ status }: Readonly<{ status: Customer["status"] }>) {
  const t = useTranslations("customers");
  return (
    <Badge color={status === "active" ? "teal" : "gray"} variant="light">
      {t(`status.${status}`)}
    </Badge>
  );
}

export function LegalTypeBadge({ legalType }: Readonly<{ legalType: Customer["legalType"] }>) {
  const t = useTranslations("customers");
  const Icon = legalType === "business" ? IconBuilding : IconUser;
  return (
    <Badge
      color={legalType === "business" ? "blue" : "grape"}
      variant="light"
      leftSection={<Icon size={12} stroke={1.5} />}
    >
      {t(`legalType.${legalType}`)}
    </Badge>
  );
}

export function CreateCustomerModal({
  opened,
  onClose,
  defaultLegalType,
}: Readonly<{
  opened: boolean;
  onClose: () => void;
  defaultLegalType?: Customer["legalType"];
}>) {
  const t = useTranslations("customers");
  const createCustomer = useCreateCustomer();
  const [warnings, setWarnings] = useState<DuplicateWarning[]>([]);

  const handleSubmit = (values: CustomerCreateInput) => {
    createCustomer.mutate(values, {
      onSuccess: ({ customer, warnings: resultWarnings }) => {
        notifications.show({
          color: resultWarnings.length > 0 ? "yellow" : "green",
          title: t("notifications.createdTitle"),
          message: t("notifications.createdMessage", { name: customer.legalName, id: customer.id }),
        });
        setWarnings([]);
        onClose();
      },
      onError: (error) => {
        notifications.show({
          color: "red",
          title: t("notifications.errorTitle"),
          message: error.message,
        });
      },
    });
  };

  return (
    <Modal
      opened={opened}
      onClose={() => {
        setWarnings([]);
        onClose();
      }}
      title={t("createTitle")}
      size="lg"
    >
      <CustomerForm
        defaultLegalType={defaultLegalType}
        isSubmitting={createCustomer.isPending}
        warnings={warnings}
        submitLabel={t("form.create")}
        onSubmit={handleSubmit}
      />
    </Modal>
  );
}

export function EditCustomerModal({
  customer,
  onClose,
}: Readonly<{ customer: Customer | null; onClose: () => void }>) {
  const t = useTranslations("customers");
  const updateCustomer = useUpdateCustomer();
  const [warnings, setWarnings] = useState<DuplicateWarning[]>([]);

  const handleSubmit = (values: CustomerCreateInput) => {
    if (!customer) return;
    updateCustomer.mutate(
      { id: customer.id, input: values },
      {
        onSuccess: ({ warnings: resultWarnings }) => {
          if (resultWarnings.length > 0) {
            setWarnings(resultWarnings);
            return;
          }
          notifications.show({
            color: "green",
            title: t("notifications.updatedTitle"),
            message: t("notifications.updatedMessage", { id: customer.id }),
          });
          setWarnings([]);
          onClose();
        },
        onError: (error) => {
          notifications.show({
            color: "red",
            title: t("notifications.errorTitle"),
            message: error.message,
          });
        },
      },
    );
  };

  return (
    <Modal
      opened={customer !== null}
      onClose={() => {
        setWarnings([]);
        onClose();
      }}
      title={customer ? t("editTitle", { id: customer.id }) : ""}
      size="lg"
    >
      {customer && (
        <CustomerForm
          initialValues={customer}
          isSubmitting={updateCustomer.isPending}
          warnings={warnings}
          submitLabel={t("form.save")}
          onSubmit={handleSubmit}
        />
      )}
    </Modal>
  );
}
