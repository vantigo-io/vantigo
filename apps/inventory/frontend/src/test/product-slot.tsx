import { useParams } from "@tanstack/react-router";
import { ProductStock } from "../components/product-stock";

/** Stands in for Products' product page, where the host draws `ProductStock` among its cards. */
export const ProductSlot = () => {
  const { productId } = useParams({ strict: false }) as { productId: string };
  return <ProductStock productId={Number(productId)} />;
};
