import Image from "next/image";

/**
 * Decorative art tile backed by picsum's seeded endpoint.
 * Fills its (relatively positioned) parent; supports the luminosity
 * blend + opacity treatment used throughout the design.
 */
export function SeededImage({
  seed,
  alt = "",
  luminosity = false,
  opacity = 1,
  sizes = "300px",
}: {
  seed: string;
  alt?: string;
  luminosity?: boolean;
  opacity?: number;
  sizes?: string;
}) {
  return (
    <Image
      src={`https://picsum.photos/seed/${seed}/600/760`}
      alt={alt}
      fill
      sizes={sizes}
      className="object-cover"
      style={{
        mixBlendMode: luminosity ? "luminosity" : undefined,
        opacity,
      }}
    />
  );
}
