import { Alert, Loader, Modal, Stack, Text, TextInput, UnstyledButton } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconSearch } from "@tabler/icons-react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  communicationsImages,
  type PickerImage,
  type PickerProduct,
  pickerImagesQueryOptions,
  pickerProductsQueryOptions,
} from "../api/products";

export type ProductPickerProps = {
  opened: boolean;
  onClose: () => void;
  /** The product picked, with its images (all of them: the draft keeps those marked for Communications). */
  onPick: (product: PickerProduct, images: PickerImage[]) => void;
};

/**
 * The product picker of *Attach product* (Messenger design N16): a search over Products' own API with
 * the user's permissions, each product with the reason it cannot be attached when none of its images
 * is marked for Communications.
 */
export function ProductPicker({ opened, onClose, onPick }: ProductPickerProps) {
  const { t } = useI18n("communications");
  const [search, setSearch] = useState("");
  const [term] = useDebouncedValue(search, 250);
  const products = useQuery({ ...pickerProductsQueryOptions(term), enabled: opened });
  const list = products.data ?? [];
  const images = useQueries({
    queries: list.map((product) => ({ ...pickerImagesQueryOptions(product.id), enabled: opened })),
  });
  return (
    <Modal opened={opened} onClose={onClose} title={t("attachProductTitle")} size="md">
      <TextInput
        aria-label={t("searchProducts")}
        placeholder={t("searchProducts")}
        leftSection={<IconSearch size={14} />}
        value={search}
        onChange={(event) => setSearch(event.currentTarget.value)}
        data-autofocus
      />
      <Stack gap={4} mt="sm">
        {products.isPending && <Loader size="sm" />}
        {products.isError && (
          <Alert color="red" variant="light">
            {t("productsNotLoaded")}
          </Alert>
        )}
        {products.isSuccess && list.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("noProductsFound")}
          </Text>
        )}
        {list.map((product, index) => {
          const loaded = images[index]?.data;
          const marked = loaded ? communicationsImages(loaded) : null;
          const unavailable = marked !== null && marked.length === 0;
          return (
            <UnstyledButton
              key={product.id}
              disabled={!loaded || unavailable}
              onClick={() => loaded && onPick(product, loaded)}
              className="product-picker-row"
              aria-label={product.name}
            >
              <Text fw={600} size="sm">
                {product.name}
              </Text>
              <Text size="xs" c="dimmed">
                {[
                  product.sku,
                  unavailable
                    ? t("productNoCommunicationsImage")
                    : marked
                      ? t("productImages", { count: marked.length })
                      : "",
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </Text>
            </UnstyledButton>
          );
        })}
      </Stack>
    </Modal>
  );
}
