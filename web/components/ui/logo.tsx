import { Send } from "lucide-react";
import { cn } from "@/lib/cn";

/** The teal gradient mark used in the nav, loader, curtain and footer. */
export function LogoMark({
  size = 40,
  radius = 13,
  iconSize = 21,
  className,
  glow = true,
}: {
  size?: number;
  radius?: number;
  iconSize?: number;
  className?: string;
  glow?: boolean;
}) {
  return (
    <span
      className={cn("flex items-center justify-center", className)}
      style={{
        width: size,
        height: size,
        borderRadius: radius,
        background: "linear-gradient(140deg,#4fe0c0,#1fa68f)",
        boxShadow: glow ? "0 8px 20px -8px rgba(31,166,143,0.6)" : undefined,
      }}
    >
      <Send size={iconSize} strokeWidth={2} color="#fff" />
    </span>
  );
}

export function Wordmark({
  name,
  size = 19,
  color = "#171715",
}: {
  name: string;
  size?: number;
  color?: string;
}) {
  return (
    <span style={{ fontWeight: 800, fontSize: size, letterSpacing: "-0.02em", color }}>
      {name}
    </span>
  );
}
