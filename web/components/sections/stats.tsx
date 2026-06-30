"use client";

import { useEffect, useRef, useState } from "react";
import { animate, useInView } from "motion/react";
import { home } from "@/lib/content";
import type { Stat } from "@/lib/types";

function Counter({ stat }: { stat: Stat }) {
  const ref = useRef<HTMLDivElement>(null);
  const inView = useInView(ref, { once: true, amount: 0.6 });
  const [value, setValue] = useState(0);

  useEffect(() => {
    if (!inView) return;
    const controls = animate(0, stat.value, {
      duration: 1.6,
      ease: [0.16, 1, 0.3, 1],
      onUpdate: (v) => setValue(v),
    });
    return () => controls.stop();
  }, [inView, stat.value]);

  const formatted = value.toLocaleString("en-US", {
    minimumFractionDigits: stat.decimals ?? 0,
    maximumFractionDigits: stat.decimals ?? 0,
  });

  return (
    <div ref={ref}>
      <div
        className="text-[clamp(34px,4vw,52px)] font-extrabold tracking-[-0.03em]"
        style={{ color: stat.accent ?? "#fff" }}
      >
        {stat.prefix}
        {formatted}
        {stat.suffix}
      </div>
      <div className="mt-2 text-sm font-medium text-white/60">{stat.label}</div>
    </div>
  );
}

export function Stats() {
  return (
    <section className="relative z-[5] mx-auto max-w-[1240px] px-6 pb-[70px] pt-[30px]">
      <div className="grid grid-cols-2 gap-[30px] rounded-[30px] bg-ink p-[54px_40px] text-center shadow-[0_40px_80px_-40px_rgba(20,20,18,0.6)] md:grid-cols-4">
        {home.stats.map((stat) => (
          <Counter key={stat.label} stat={stat} />
        ))}
      </div>
    </section>
  );
}
