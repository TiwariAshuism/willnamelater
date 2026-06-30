"use client";

import { useState } from "react";
import { motion } from "motion/react";
import { Brush, GitMerge, RotateCcw, Send, Boxes, Sparkles, Star, Plus } from "lucide-react";
import { home } from "@/lib/content";
import { SeededImage } from "@/components/ui/seeded-image";

const FLOATERS = [
  { icon: Brush, bg: "#fff", color: "#171715" },
  { icon: GitMerge, bg: "#171715", color: "#fff" },
  { icon: RotateCcw, bg: "#fff", color: "#e6522f" },
  { icon: Send, bg: "#fff", color: "#171715" },
  { icon: Boxes, bg: "#2f6bff", color: "#fff" },
  { icon: Sparkles, bg: "#fff", color: "#1fa68f" },
  { icon: Star, bg: "#f0c531", color: "#171715" },
];

const GRID = [
  { bg: "#1f7a44", label: "STAFF" },
  { bg: "#d98a3c", seed: "ross-v2" },
  { bg: "#2c4a2f", seed: "ross-v3" },
  { bg: "#caa37a", seed: "ross-v4" },
  { bg: "#e85d7a", seed: "ross-v5" },
  { bg: "#5b8def", gradient: true },
];

export function Vision() {
  const { vision } = home;
  const [tab, setTab] = useState(0);

  return (
    <section className="relative z-[5] mx-auto grid max-w-[1240px] grid-cols-1 items-center gap-14 px-6 py-[90px] md:grid-cols-2">
      <div>
        <div className="flex h-[46px] w-[46px] items-center justify-center rounded-[14px] bg-white shadow-[0_10px_24px_-10px_rgba(20,20,18,0.28)]">
          <Sparkles size={22} color="#171715" strokeWidth={2} />
        </div>
        <h2 className="mt-[22px] max-w-[11ch] text-[clamp(34px,4.4vw,56px)] font-extrabold leading-[1.04] tracking-[-0.03em]">
          {vision.title}
        </h2>
        <p className="mt-[22px] max-w-[40ch] text-base leading-[1.55] text-muted">{vision.body}</p>
        <div className="mt-9 flex max-w-[380px] flex-wrap gap-4">
          {FLOATERS.map((f, i) => {
            const Ico = f.icon;
            return (
              <div
                key={i}
                className="flex h-16 w-16 items-center justify-center rounded-full shadow-[0_14px_30px_-14px_rgba(20,20,18,0.3)] animate-float"
                style={{ background: f.bg, animationDelay: `${i * 0.3}s` }}
              >
                <Ico size={24} color={f.color} strokeWidth={1.9} />
              </div>
            );
          })}
        </div>
      </div>

      <div className="rounded-[26px] bg-white p-[22px] shadow-[0_34px_70px_-30px_rgba(20,20,18,0.4)]">
        <div className="mb-[18px] flex items-center justify-between">
          <div className="relative flex rounded-full bg-canvas p-[5px]">
            {vision.tabs.map((t, i) => (
              <button
                key={t}
                onClick={() => setTab(i)}
                className="relative z-[1] rounded-full px-5 py-[9px] text-sm font-semibold transition-colors"
                style={{ color: tab === i ? "#fff" : "rgba(20,20,18,0.55)" }}
              >
                {tab === i && (
                  <motion.span
                    layoutId="vision-tab"
                    className="absolute inset-0 -z-[1] rounded-full bg-ink"
                    transition={{ type: "spring", bounce: 0.2, duration: 0.5 }}
                  />
                )}
                {t}
              </button>
            ))}
          </div>
          <button className="flex cursor-pointer items-center gap-1.5 rounded-full bg-canvas px-[15px] py-[9px] text-[13px] font-semibold text-ink transition hover:scale-[1.04]">
            <Plus size={14} color="#171715" strokeWidth={2.4} />
            Create
          </button>
        </div>
        <div className="grid grid-cols-3 gap-3">
          {GRID.map((c, i) => (
            <div
              key={i}
              className="relative aspect-square overflow-hidden rounded-[14px]"
              style={{
                background: c.gradient ? "radial-gradient(circle at 40% 30%,#cfe0ff,#5b8def)" : c.bg,
              }}
            >
              {c.seed && <SeededImage seed={c.seed} luminosity opacity={0.9} sizes="120px" />}
              {c.label && (
                <span className="absolute inset-x-0 bottom-2 text-center text-[11px] font-extrabold text-[#eafff2]">
                  {c.label}
                </span>
              )}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
