"use client";

import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { ArrowRight, Play, Sparkles } from "lucide-react";
import { home } from "@/lib/content";
import { Reveal, Headline } from "@/components/ui/reveal";

function Reel() {
  const items = home.bento.reel;
  const [i, setI] = useState(0);
  useEffect(() => {
    const t = setInterval(() => setI((p) => (p + 1) % items.length), 1900);
    return () => clearInterval(t);
  }, [items.length]);
  return (
    <div className="relative mt-2.5 min-h-[300px] flex-1">
      <AnimatePresence mode="wait">
        <motion.div
          key={i}
          className="absolute inset-x-0 top-1/2 text-center text-[22px] tracking-[-0.02em]"
          initial={{ opacity: 0, y: 28 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: -28 }}
          transition={{ duration: 0.5, ease: [0.16, 1, 0.3, 1] }}
        >
          {items[i].text}
        </motion.div>
      </AnimatePresence>
    </div>
  );
}

export function StoryBento() {
  const { bento } = home;
  return (
    <section className="relative z-[5] mx-auto max-w-[1240px] px-6 py-[90px]">
      <div className="mb-11 text-center">
        <div className="text-xs font-bold tracking-[0.16em] text-faint">
          {bento.eyebrowParts.map((p, i) => (
            <span key={i} style={{ color: p.accent ? "var(--color-blue)" : undefined }}>
              {p.text}
            </span>
          ))}
        </div>
        <h2 className="mt-3.5 text-[clamp(32px,4.2vw,54px)] font-extrabold tracking-[-0.03em]">
          <Headline parts={[{ text: bento.title }]} accentColor="#171715" />
        </h2>
      </div>

      <div className="grid grid-cols-1 gap-[18px] md:grid-cols-[1.15fr_1fr_0.85fr]">
        {/* card 1 */}
        <Reveal className="rounded-[24px] bg-white p-[22px] shadow-[0_26px_54px_-26px_rgba(20,20,18,0.35)] md:col-start-1 md:row-start-1">
          <div className="relative flex h-[178px] items-center justify-center">
            <div className="absolute right-6 top-0 z-20 rounded-[12px_12px_4px_12px] bg-ink px-[11px] py-1.5 text-xs font-semibold text-white">
              @robin
            </div>
            <div
              className="absolute h-[150px] w-[118px] overflow-hidden rounded-[14px] border-4 border-white shadow-[0_16px_32px_-16px_rgba(0,0,0,0.5)]"
              style={{ transform: "rotate(-12deg) translateX(-40px)", background: "#caa37a" }}
            >
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src="https://picsum.photos/seed/ross-s1a/160/200" alt="" className="h-full w-full object-cover" />
            </div>
            <div
              className="absolute h-[150px] w-[118px] overflow-hidden rounded-[14px] border-4 border-white shadow-[0_16px_32px_-16px_rgba(0,0,0,0.5)]"
              style={{ transform: "rotate(10deg) translateX(40px)", background: "#e85d7a" }}
            >
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src="https://picsum.photos/seed/ross-s1b/160/200" alt="" className="h-full w-full object-cover opacity-90 [mix-blend-mode:luminosity]" />
            </div>
            <div className="absolute z-[5] flex h-[154px] w-[122px] items-center justify-center rounded-[14px] border-4 border-white bg-ink shadow-[0_18px_36px_-16px_rgba(0,0,0,0.55)]">
              <span className="text-[15px] font-extrabold tracking-[0.18em] text-white">PRADA</span>
            </div>
            <button className="absolute -bottom-1.5 left-1/2 z-10 flex -translate-x-1/2 cursor-pointer items-center gap-2 rounded-full bg-white py-[9px] pl-[11px] pr-4 text-[13px] font-semibold text-ink shadow-[0_12px_26px_-10px_rgba(0,0,0,0.4)]">
              <span className="flex h-[22px] w-[22px] items-center justify-center rounded-full bg-orange">
                <Play size={10} fill="#fff" color="#fff" />
              </span>
              Play Video
            </button>
          </div>
          <h3 className="mt-[22px] text-[21px] font-extrabold tracking-[-0.02em]">Connect, Create, Commerce</h3>
          <p className="mt-2 text-sm leading-[1.5] text-muted">One continuous flow from the first brushstroke to the final sale.</p>
          <a href="#" className="mt-3.5 inline-flex items-center gap-1.5 text-sm font-semibold text-ink no-underline">
            How it works? <ArrowRight size={15} strokeWidth={2.2} />
          </a>
        </Reveal>

        {/* card 2 */}
        <Reveal className="overflow-hidden rounded-[24px] bg-[#3a4ad6] p-[22px] text-white shadow-[0_26px_54px_-26px_rgba(58,74,214,0.5)] md:col-start-2 md:row-start-1">
          <div className="relative flex h-[178px] items-center justify-center">
            <div
              className="relative h-[130px] w-[130px] rounded-full shadow-[0_0_0_10px_rgba(255,255,255,0.06)]"
              style={{
                background:
                  "radial-gradient(circle at 50% 50%,#fff 0 14%,#ffd23f 14% 24%,#3a4ad6 24% 40%,#7d8bff 40% 56%,#2230a8 56%)",
              }}
            />
          </div>
          <h3 className="mt-[22px] text-[21px] font-extrabold tracking-[-0.02em]">Where Art Breathes Commerce</h3>
          <p className="mt-2 text-sm leading-[1.5] text-white/70">Liquidity for creativity — every view is a chance to be collected.</p>
          <a href="#" className="mt-3.5 inline-flex items-center gap-1.5 text-sm font-semibold text-white no-underline">
            Read more <ArrowRight size={15} strokeWidth={2.2} />
          </a>
        </Reveal>

        {/* card 4 — reel */}
        <Reveal className="relative flex flex-col overflow-hidden rounded-[24px] bg-ink p-6 text-white shadow-[0_26px_54px_-26px_rgba(20,20,18,0.6)] md:col-start-3 md:row-span-2 md:row-start-1">
          <div className="flex items-center justify-between">
            <span className="text-[11px] font-bold tracking-[0.16em] text-white/50">ADVANTAGES</span>
            <Sparkles size={18} color="rgba(255,255,255,0.6)" strokeWidth={2} />
          </div>
          <Reel />
        </Reveal>

        {/* card 3 */}
        <Reveal className="flex items-center gap-[22px] rounded-[24px] bg-white p-[22px] shadow-[0_26px_54px_-26px_rgba(20,20,18,0.35)] md:col-span-2 md:col-start-1 md:row-start-2">
          <div
            className="relative h-[178px] w-[150px] flex-none overflow-hidden rounded-[16px] border-[5px] border-ink"
            style={{ background: "repeating-conic-gradient(#171715 0 25%,#fff 0 50%) 0 0/26px 26px" }}
          >
            <div className="absolute inset-2 overflow-hidden rounded-[10px] bg-green-bright">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src="https://picsum.photos/seed/ross-pop/200/240" alt="" className="h-full w-full object-cover opacity-80 [mix-blend-mode:luminosity]" />
              <div className="absolute right-2 top-2 flex h-[30px] w-[30px] items-center justify-center rounded-full bg-yellow text-base">☺</div>
            </div>
          </div>
          <div>
            <h3 className="m-0 text-[21px] font-extrabold tracking-[-0.02em]">Spin Your Art into Gold</h3>
            <p className="mt-2 max-w-[34ch] text-sm leading-[1.5] text-muted">Turn a following into a market. Collectors are one tap away.</p>
            <button className="mt-4 cursor-pointer rounded-full bg-ink px-[22px] py-3 text-sm font-semibold text-white transition hover:scale-[1.03]">
              Join us now
            </button>
          </div>
        </Reveal>
      </div>
    </section>
  );
}
