"use client";

import { motion } from "motion/react";
import { ArrowRight } from "lucide-react";
import { home } from "@/lib/content";
import { SeededImage } from "@/components/ui/seeded-image";
import { words } from "@/lib/words";

const EASE = [0.16, 1, 0.3, 1] as const;

export function Hero() {
  const { hero } = home;
  const n = hero.fan.length;
  const center = (n - 1) / 2;

  return (
    <section className="relative z-[5] mx-auto flex max-w-[1240px] flex-col items-center px-6 pb-20 pt-[34px] text-center">
      <h1 className="m-0 max-w-[14ch] text-[clamp(40px,6.6vw,88px)] font-extrabold leading-[1.02] tracking-[-0.035em]">
        {words(hero.headlineLead).map((w, i) => (
          <motion.span
            key={i}
            className="inline-block"
            initial={{ opacity: 0, y: 24, filter: "blur(9px)" }}
            animate={{ opacity: 1, y: 0, filter: "blur(0px)" }}
            transition={{ duration: 0.85, ease: EASE, delay: 1.5 + i * 0.05 }}
          >
            {w}&nbsp;
          </motion.span>
        ))}
        <br />
        <motion.span
          className="inline-block text-[#bdbdb8]"
          initial={{ opacity: 0, y: 24, filter: "blur(9px)" }}
          animate={{ opacity: 1, y: 0, filter: "blur(0px)" }}
          transition={{ duration: 0.85, ease: EASE, delay: 1.5 + words(hero.headlineLead).length * 0.05 }}
        >
          {hero.headlineAccent}
        </motion.span>
      </h1>

      {/* fanned art cards */}
      <div className="relative mx-auto mt-[46px] flex h-[340px] w-full max-w-[760px] items-center justify-center">
        {hero.fan.map((card, i) => {
          const offset = i - center;
          const rotate = offset * 11;
          const x = offset * 92;
          const y = Math.abs(offset) * Math.abs(offset) * 8 - 20;
          return (
            <motion.div
              key={i}
              className="absolute h-[236px] w-[188px] overflow-hidden rounded-[22px] border-[5px] border-white shadow-[0_22px_44px_-18px_rgba(20,20,18,0.5)]"
              style={{ background: card.bg }}
              initial={{ rotate: 0, x: 0, y: 60, opacity: 0 }}
              animate={{ rotate, x, y, opacity: 1 }}
              transition={{ duration: 1, ease: EASE, delay: 1.7 + i * 0.07 }}
            >
              {card.seed && (
                <SeededImage seed={card.seed} luminosity={card.bg !== "#ffffff"} opacity={card.bg === "#ffffff" ? 1 : 0.9} sizes="188px" />
              )}
              {card.label && (
                <span className="absolute bottom-[11px] left-3 text-[15px] font-extrabold tracking-[-0.02em] text-[#eafff2]">
                  {card.label}
                </span>
              )}
            </motion.div>
          );
        })}
        {hero.bubbles.map((b, i) => (
          <motion.div
            key={i}
            className="absolute top-[6%] z-20 rounded-[14px] px-[13px] py-[7px] text-[13px] font-semibold text-white"
            style={{ background: b.bg, [b.side === "left" ? "left" : "right"]: "6%" }}
            initial={{ opacity: 0, scale: 0.6 }}
            animate={{ opacity: 1, scale: 1 }}
            transition={{ delay: 2.4 + i * 0.15, ease: EASE }}
          >
            {b.text}
          </motion.div>
        ))}
      </div>

      <motion.p
        className="mx-auto mt-[38px] max-w-[46ch] text-[17px] font-normal leading-[1.55] text-muted"
        initial={{ opacity: 0, y: 20 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ delay: 2.6, duration: 0.9, ease: EASE }}
      >
        {hero.body}
      </motion.p>

      <motion.div
        className="mt-[26px] flex items-center gap-[18px]"
        initial={{ opacity: 0, y: 20 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ delay: 2.75, duration: 0.9, ease: EASE }}
      >
        <button className="cursor-pointer rounded-full bg-ink px-[26px] py-[15px] text-[15px] font-semibold text-white shadow-[0_14px_30px_-12px_rgba(20,20,18,0.55)] transition hover:scale-[1.03]">
          {hero.primaryCta.label}
        </button>
        <a href={hero.secondaryCta.href} className="flex items-center gap-[7px] text-[15px] font-semibold text-ink no-underline">
          {hero.secondaryCta.label} <ArrowRight size={16} strokeWidth={2.2} />
        </a>
      </motion.div>
    </section>
  );
}
