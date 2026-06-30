"use client";

import { motion, type Variants } from "motion/react";
import type { ReactNode } from "react";

const EASE = [0.16, 1, 0.3, 1] as const;
const VIEWPORT = { once: true, amount: 0.2 } as const;

/** Generic block fade-in on scroll into view. */
export function Reveal({
  children,
  className,
  delay = 0,
  as = "div",
}: {
  children: ReactNode;
  className?: string;
  delay?: number;
  as?: "div" | "p" | "span" | "section";
}) {
  const MotionTag = motion[as];
  return (
    <MotionTag
      className={className}
      initial={{ opacity: 0, y: 26, filter: "blur(8px)" }}
      whileInView={{ opacity: 1, y: 0, filter: "blur(0px)" }}
      viewport={VIEWPORT}
      transition={{ duration: 0.9, ease: EASE, delay }}
    >
      {children}
    </MotionTag>
  );
}

/** Word-by-word headline reveal. Renders inline parts that animate in sequence. */
export function RevealWords({
  parts,
  className,
}: {
  parts: ReactNode[];
  className?: string;
}) {
  const container: Variants = {
    hidden: {},
    show: { transition: { staggerChildren: 0.045 } },
  };
  const word: Variants = {
    hidden: { opacity: 0, y: 22, filter: "blur(9px)" },
    show: { opacity: 1, y: 0, filter: "blur(0px)", transition: { duration: 0.85, ease: EASE } },
  };
  return (
    <motion.span
      className={className}
      variants={container}
      initial="hidden"
      whileInView="show"
      viewport={VIEWPORT}
    >
      {parts.map((part, i) => (
        <motion.span key={i} variants={word} style={{ display: "inline-block" }}>
          {part}
          {i < parts.length - 1 ? " " : null}
        </motion.span>
      ))}
    </motion.span>
  );
}

/**
 * Headline built from JSON parts (some flagged `accent`), revealed
 * word-by-word as it scrolls into view.
 */
export function Headline({
  parts,
  className,
  accentColor = "var(--color-orange)",
}: {
  parts: { text: string; accent?: boolean }[];
  className?: string;
  accentColor?: string;
}) {
  const container: Variants = {
    hidden: {},
    show: { transition: { staggerChildren: 0.045 } },
  };
  const word: Variants = {
    hidden: { opacity: 0, y: 22, filter: "blur(9px)" },
    show: { opacity: 1, y: 0, filter: "blur(0px)", transition: { duration: 0.85, ease: EASE } },
  };

  const tokens = parts.flatMap((part) =>
    part.text
      .split(/(\s+)/)
      .filter((t) => t.length > 0)
      .map((t) => ({ text: t, accent: part.accent }))
  );

  return (
    <motion.span
      className={className}
      variants={container}
      initial="hidden"
      whileInView="show"
      viewport={VIEWPORT}
    >
      {tokens.map((tok, i) =>
        tok.text.trim() === "" ? (
          " "
        ) : (
          <motion.span
            key={i}
            variants={word}
            style={{ display: "inline-block", color: tok.accent ? accentColor : undefined }}
          >
            {tok.text}
          </motion.span>
        )
      )}
    </motion.span>
  );
}

/** Container that staggers its direct children into view. */
export function Stagger({
  children,
  className,
  style,
}: {
  children: ReactNode;
  className?: string;
  style?: React.CSSProperties;
}) {
  return (
    <motion.div
      className={className}
      style={style}
      initial="hidden"
      whileInView="show"
      viewport={VIEWPORT}
      variants={{ hidden: {}, show: { transition: { staggerChildren: 0.09 } } }}
    >
      {children}
    </motion.div>
  );
}

export function StaggerItem({
  children,
  className,
  style,
}: {
  children: ReactNode;
  className?: string;
  style?: React.CSSProperties;
}) {
  return (
    <motion.div
      className={className}
      style={style}
      variants={{
        hidden: { opacity: 0, y: 26, filter: "blur(6px)" },
        show: { opacity: 1, y: 0, filter: "blur(0px)", transition: { duration: 0.8, ease: EASE } },
      }}
    >
      {children}
    </motion.div>
  );
}
