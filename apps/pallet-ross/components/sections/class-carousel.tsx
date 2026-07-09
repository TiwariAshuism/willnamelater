"use client";

import { useState } from "react";
import { motion, AnimatePresence } from "motion/react";
import { ChevronLeft, ChevronRight, Play } from "lucide-react";
import { home } from "@/lib/content";

const SLIDES = [
  { bg: "#e8693f", seed: "ross-c1" },
  { bg: "#2f6bff", seed: "ross-c2" },
  { bg: "#1f7a44", seed: "ross-c3" },
];

export function ClassCarousel() {
  const { classSection } = home;
  const [i, setI] = useState(0);
  const n = SLIDES.length;
  const go = (d: number) => setI((p) => (p + d + n) % n);
  const author = classSection.authors[i % classSection.authors.length];

  return (
    <section className="relative z-[5] mx-auto max-w-[1240px] px-6 pb-[90px] pt-[60px]">
      <div className="mb-[30px] flex flex-wrap items-end justify-between gap-5">
        <div>
          <div className="text-xs font-bold tracking-[0.16em] text-faint">
            {classSection.eyebrowPrefix} {author}
          </div>
          <h2 className="mt-3.5 flex flex-wrap items-center gap-[18px] text-[clamp(32px,4.4vw,56px)] font-extrabold leading-[1.04] tracking-[-0.03em]">
            {classSection.title}
          </h2>
        </div>
        <div className="rounded-[14px_14px_4px_14px] bg-ink px-3.5 py-2 text-[13px] font-semibold text-white shadow-[0_12px_26px_-12px_rgba(20,20,18,0.6)]">
          {classSection.bubble}
        </div>
      </div>

      <div className="relative aspect-[16/7] w-full overflow-hidden rounded-[26px] shadow-[0_30px_70px_-30px_rgba(20,20,18,0.45)]">
        <AnimatePresence initial={false}>
          <motion.div
            key={i}
            className="absolute inset-0"
            style={{ background: SLIDES[i].bg }}
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.6 }}
          >
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={`https://picsum.photos/seed/${SLIDES[i].seed}/1200/520`}
              alt=""
              className="h-full w-full object-cover opacity-[0.82] [mix-blend-mode:luminosity]"
            />
          </motion.div>
        </AnimatePresence>

        <div className="absolute left-6 top-6 z-[5] flex flex-col gap-2">
          <div className="h-10 w-10 rounded-[13px] bg-white shadow-[0_8px_18px_-8px_rgba(0,0,0,0.4)]" />
          <div className="h-10 w-10 rounded-[13px] bg-ink shadow-[0_8px_18px_-8px_rgba(0,0,0,0.4)]" />
        </div>

        <div className="absolute right-6 top-[30px] z-[5] flex gap-[7px]">
          {SLIDES.map((_, d) => (
            <span
              key={d}
              className="h-2 rounded-full transition-all"
              style={{ width: d === i ? 26 : 8, background: d === i ? "#171715" : "rgba(20,20,18,0.25)" }}
            />
          ))}
        </div>

        <button className="absolute bottom-6 left-6 z-[5] flex cursor-pointer items-center gap-[9px] rounded-full bg-white py-[11px] pl-[15px] pr-[19px] text-sm font-semibold text-ink shadow-[0_10px_24px_-10px_rgba(0,0,0,0.5)] transition hover:scale-[1.03]">
          <span className="flex h-6 w-6 items-center justify-center rounded-full bg-ink">
            <Play size={11} fill="#fff" color="#fff" />
          </span>
          Watch
        </button>

        <div className="absolute bottom-6 right-6 z-[5] flex gap-2.5">
          <button
            onClick={() => go(-1)}
            aria-label="Previous"
            className="flex h-[46px] w-[46px] cursor-pointer items-center justify-center rounded-full bg-white/90 shadow-[0_8px_20px_-8px_rgba(0,0,0,0.5)] transition hover:scale-[1.06]"
          >
            <ChevronLeft size={18} color="#171715" strokeWidth={2.4} />
          </button>
          <button
            onClick={() => go(1)}
            aria-label="Next"
            className="flex h-[46px] w-[46px] cursor-pointer items-center justify-center rounded-full bg-white/90 shadow-[0_8px_20px_-8px_rgba(0,0,0,0.5)] transition hover:scale-[1.06]"
          >
            <ChevronRight size={18} color="#171715" strokeWidth={2.4} />
          </button>
        </div>
      </div>
    </section>
  );
}
