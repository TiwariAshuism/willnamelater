import { ArrowRight } from "lucide-react";
import { home } from "@/lib/content";
import { Reveal, Headline } from "@/components/ui/reveal";
import { SeededImage } from "@/components/ui/seeded-image";

const STACK = [
  { left: 18, top: 40, rotate: -13, bg: "#1f7a44", seed: "ross-e1" },
  { left: 120, top: 18, rotate: -3, bg: "#ffffff", seed: "ross-e2" },
  { left: 222, top: 30, rotate: 9, bg: "#e6522f", seed: "ross-e3" },
  { left: 70, top: 150, rotate: -8, bg: "#2f6bff", seed: "ross-e4" },
  { left: 180, top: 158, rotate: 6, bg: "#f0c531", seed: "ross-e5" },
];

export function Ecommerce() {
  const { ecommerce } = home;
  return (
    <section className="relative z-[5] mx-auto mt-10 grid max-w-[1240px] grid-cols-1 items-center gap-10 px-6 pb-[120px] pt-20 md:grid-cols-[1.05fr_0.95fr]">
      <div>
        <Reveal className="inline-flex items-center gap-2 rounded-full bg-orange/10 px-[13px] py-[7px] text-xs font-bold tracking-[0.16em] text-orange">
          {ecommerce.eyebrow}
        </Reveal>
        <h2 className="mt-5 max-w-[13ch] text-[clamp(34px,4.4vw,58px)] font-extrabold leading-[1.05] tracking-[-0.03em]">
          <Headline parts={ecommerce.headParts} />
        </h2>
        <Reveal as="p" className="mt-6 max-w-[42ch] text-base leading-[1.55] text-muted">
          {ecommerce.body}
        </Reveal>
        <Reveal className="mt-7 flex items-center gap-[18px]">
          <button className="cursor-pointer rounded-full bg-ink px-[26px] py-[15px] text-[15px] font-semibold text-white shadow-[0_14px_30px_-12px_rgba(20,20,18,0.55)] transition hover:scale-[1.03]">
            {ecommerce.primaryCta.label}
          </button>
          <a href={ecommerce.secondaryCta.href} className="flex items-center gap-[7px] text-[15px] font-semibold text-ink no-underline">
            {ecommerce.secondaryCta.label} <ArrowRight size={16} strokeWidth={2.2} />
          </a>
        </Reveal>
      </div>

      <div className="relative h-[460px]">
        <div className="absolute right-0 top-0 z-10 h-[420px] w-[430px]">
          <div className="absolute -top-[22px] left-[30px] z-30 rounded-[14px_14px_14px_4px] bg-[#8c1f1f] px-[13px] py-[7px] text-[13px] font-semibold text-white shadow-[0_10px_24px_-10px_rgba(140,31,31,0.7)]">
            @howard
          </div>
          {STACK.map((c, i) => (
            <div
              key={i}
              className="absolute h-[216px] w-[172px] overflow-hidden rounded-[20px] border-[5px] border-white shadow-[0_24px_50px_-20px_rgba(20,20,18,0.55)]"
              style={{ left: c.left, top: c.top, transform: `rotate(${c.rotate}deg)`, background: c.bg }}
            >
              <SeededImage seed={c.seed} luminosity={c.bg !== "#ffffff"} opacity={c.bg === "#ffffff" ? 1 : 0.9} sizes="172px" />
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
