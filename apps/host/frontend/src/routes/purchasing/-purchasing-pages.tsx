import { Alert, Modal } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams, useSearch } from "@tanstack/react-router";
import { supplierQueryOptions } from "@vantigo/accounting-ui/api/accounting";
import { SupplierFormModal } from "@vantigo/accounting-ui/pages/suppliers";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import {
  PURCHASING_QUERY_KEY,
  type PurchasingAccess,
  type RenderSupplierForm,
  type SupplierFormSlotProps,
} from "@vantigo/purchasing-ui";
import { purchasingSupplierQueryOptions } from "@vantigo/purchasing-ui/api/purchasing";
import { usePurchasingFormat } from "@vantigo/purchasing-ui/lib/format";
import { NewPurchaseOrderPage, PurchaseOrderPage } from "@vantigo/purchasing-ui/pages/order";
import { PurchaseOrdersPage } from "@vantigo/purchasing-ui/pages/orders";
import { PurchasingSettingsPage } from "@vantigo/purchasing-ui/pages/settings";
import { SupplierPage, SuppliersPage } from "@vantigo/purchasing-ui/pages/suppliers";
import type { ReactNode } from "react";
import { usePurchasingAccess } from "../../lib/purchasing-access";
import { hasAllPermissions } from "../../navigation";

/**
 * The Purchasing app's pages, each told what the caller may do (purchasing
 * design §8) once the root layout's session has answered, and the suppliers'
 * pages handed the register's form. They sit beside the route files because a
 * route file may export nothing but its `Route`.
 */
const withAccess = (render: (access: PurchasingAccess, renderSupplierForm: RenderSupplierForm) => ReactNode) =>
  function PurchasingRoutePage() {
    const { ready, access, permissions } = usePurchasingAccess();
    if (!ready) return <ContentSkeleton rows={4} rowHeight={48} />;
    // A poster of the books keeps every field of the register's form; a buyer
    // (accounting:suppliers-manage) all but the books' own (A12, A26).
    const books = hasAllPermissions(permissions, ["accounting:post", "accounting:read"]);
    return (
      <>
        {render(access, (props) => (
          <RegisterSupplierForm {...props} books={books} />
        ))}
      </>
    );
  };

/**
 * The register's supplier form, Accounting's own, composed here because a
 * module package imports no other (P13). Purchasing offers it only when its
 * meta says the caller may write the register. A change reads the supplier
 * first, for its values and revision: a poster of the books through
 * Accounting, whose read has the books' fields; a buyer — who may hold
 * accounting:suppliers-manage alone, without accounting:read — through
 * Purchasing's own supplier read, which answers the register's revision (A26).
 * A read that fails says so, rather than opening nothing.
 */
const RegisterSupplierForm = ({ supplierId, onClose, onSaved, books }: SupplierFormSlotProps & { books: boolean }) => {
  const { t } = usePurchasingFormat();
  const queryClient = useQueryClient();
  const editing = supplierId !== undefined;
  const fromBooks = useQuery({ ...supplierQueryOptions(supplierId ?? 0), enabled: editing && books });
  const fromPurchasing = useQuery({ ...purchasingSupplierQueryOptions(supplierId ?? 0), enabled: editing && !books });
  const current = books ? fromBooks : fromPurchasing;
  if (editing && current.isError) {
    return (
      <Modal opened onClose={onClose} title={t("editSupplier")}>
        <Alert color="red">{t("supplierReadFailed")}</Alert>
      </Modal>
    );
  }
  if (editing && !current.data) return null;
  return (
    <SupplierFormModal
      supplier={current.data}
      books={books}
      onClose={onClose}
      onSaved={async (saved) => {
        await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
        onSaved(saved.id);
      }}
    />
  );
};

export const PurchaseOrdersRoutePage = withAccess((access) => <PurchaseOrdersPage access={access} />);
export const SettingsRoutePage = withAccess((access) => <PurchasingSettingsPage access={access} />);
export const SuppliersRoutePage = withAccess((_access, renderSupplierForm) => (
  <SuppliersPage renderSupplierForm={renderSupplierForm} />
));

const PurchaseOrder = ({ access }: { access: PurchasingAccess }) => {
  const { orderId } = useParams({ from: "/purchasing/orders/$orderId" });
  return <PurchaseOrderPage orderId={Number(orderId)} access={access} />;
};
const NewPurchaseOrder = ({ access }: { access: PurchasingAccess }) => {
  const { supplierId } = useSearch({ from: "/purchasing/orders/new" });
  return <NewPurchaseOrderPage supplierId={supplierId} access={access} />;
};
const Supplier = ({
  access,
  renderSupplierForm,
}: {
  access: PurchasingAccess;
  renderSupplierForm: RenderSupplierForm;
}) => {
  const { supplierId } = useParams({ from: "/purchasing/suppliers/$supplierId" });
  return <SupplierPage supplierId={Number(supplierId)} access={access} renderSupplierForm={renderSupplierForm} />;
};

export const PurchaseOrderRoutePage = withAccess((access) => <PurchaseOrder access={access} />);
export const NewPurchaseOrderRoutePage = withAccess((access) => <NewPurchaseOrder access={access} />);
export const SupplierRoutePage = withAccess((access, renderSupplierForm) => (
  <Supplier access={access} renderSupplierForm={renderSupplierForm} />
));
