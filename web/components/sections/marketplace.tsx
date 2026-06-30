"use client";

import { useRef, useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { home } from "@/lib/content";
import type { MarketItem } from "@/lib/types";

const CARD = 320; // width + gap

function Tile({ item }: { item: MarketItem }) {
  return (
    <div className="w-[300px] flex-none">
      <div
        className="aspect-square overflow-hidden rounded-[22px] shadow-[0_22px_46px_-22px_rgba(20,20,18,0.4)]"
        style={{
          background:
            item.pattern === "spectral"
              ? "conic-gradient(from 200deg,#ff5d73,#ffb347,#3fd2c7,#5b8def,#a06bff,#ff5d73)"
              : item.bg,
        }}
      >
        {item.pattern === "dots" && (
          <div
            className="h-full w-full"
            style={{
              backgroundColor: "#171715",
              backgroundImage: "radial-gradient(#fff 1.6px,transparent 2px)",
              backgroundSize: "16px 16px",
            }}
          />
        )}
        {item.seed && (
          /* eslint-disable-next-line @next/next/no-img-element */
          <img
            src={`https://picsum.photos/seed/${item.seed}/400/400`}
            alt={item.title}
            className="h-full w-full object-cover opacity-[0.88] [mix-blend-mode:luminosity]"
          />
        )}
      </div>
      <div className="mt-3 text-base font-bold tracking-[-0.01em]">{item.title}</div>
      <div className="text-[13px] text-faint">{item.desc}</div>
    </div>
  );
}

export function Marketplace() {
  const { marketplace } = home;
  const trackRef = useRef<HTMLDivElement>(null);
  const [progress, setProgress] = useState(20);

  const scrollBy = (dir: number) => {
    const el = trackRef.current;
    if (!el) return;
    el.scrollBy({ left: dir * CARD, behavior: "smooth" });
  };
  const onScroll = () => {
    const el = trackRef.current;
    if (!el) return;
    const max = el.scrollWidth - el.clientWidth;
    const pct = max > 0 ? (el.scrollLeft / max) * 80 + 20 : 20;
    setProgress(pct);
  };

  return (
    <section className="relative z-[5] mx-auto max-w-[1240px] px-6 pb-[90px] pt-[60px]">
      <div className="mb-[34px] flex flex-wrap items-end justify-between gap-5">
        <div>
          <div className="text-xs font-bold tracking-[0.16em] text-faint">
            {marketplace.eyebrowParts.map((p, i) => (
              <span key={i} style={{ color: p.accent ? "var(--color-purple)" : undefined }}>
                {p.text}
              </span>
            ))}
          </div>
          <h2 className="mt-3.5 text-[clamp(32px,4.2vw,54px)] font-extrabold tracking-[-0.03em]">
            {marketplace.title}
          </h2>
          <p className="mt-3.5 max-w-[44ch] text-base leading-[1.55] text-muted">{marketplace.body}</p>
        </div>
        <button className="cursor-pointer rounded-full bg-purple px-6 py-[13px] text-sm font-semibold text-white shadow-[0_14px_30px_-12px_rgba(124,92,255,0.6)] transition hover:scale-[1.03]">
          View All
        </button>
      </div>

      <div
        ref={trackRef}
        onScroll={onScroll}
        className="no-scrollbar flex gap-5 overflow-x-auto px-0.5 pb-2.5 pt-1.5"
      >
        {marketplace.items.map((item) => (
          <Tile key={item.title} item={item} />
        ))}
      </div>

      <div className="mt-6 flex items-center gap-5">
        <div className="flex gap-2.5">
          <button
            onClick={() => scrollBy(-1)}
            aria-label="Previous"
            className="flex h-[46px] w-[46px] cursor-pointer items-center justify-center rounded-full border-[1.5px] border-ink/15 bg-white transition hover:scale-[1.06]"
          >
            <ChevronLeft size={18} color="#171715" strokeWidth={2.4} />
          </button>
          <button
            onClick={() => scrollBy(1)}
            aria-label="Next"
            className="flex h-[46px] w-[46px] cursor-pointer items-center justify-center rounded-full border-[1.5px] border-ink/15 bg-white transition hover:scale-[1.06]"
          >
            <ChevronRight size={18} color="#171715" strokeWidth={2.4} />
          </button>
        </div>
        <div className="h-[5px] flex-1 overflow-hidden rounded-full bg-ink/10">
          <div className="h-full rounded-full bg-ink transition-[width] duration-200" style={{ width: `${progress}%` }} />
        </div>
      </div>
    </section>
  );
}
