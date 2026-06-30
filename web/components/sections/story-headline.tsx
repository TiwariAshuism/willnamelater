import { Target, Send } from "lucide-react";
import { home } from "@/lib/content";
import { Reveal, Stagger, StaggerItem } from "@/components/ui/reveal";
import { SeededImage } from "@/components/ui/seeded-image";

const CARDS = [
  { rotate: -15, x: -150, bg: "#3a4ad6", seed: "ross-t1" },
  { rotate: -7, x: -78, bg: "#ffffff", seed: "ross-t2" },
  { center: true },
  { rotate: 7, x: 78, bg: "#e6522f", seed: "ross-t4" },
  { rotate: 15, x: 150, bg: "#f0c531", seed: "ross-t5" },
];

export function StoryHeadline() {
  const { storyHeadline } = home;
  return (
    <section className="relative z-[5] mx-auto max-w-[1100px] px-6 pb-[70px] pt-[90px] text-center">
      <Reveal
        as="span"
        className="mx-auto block max-w-[20ch] text-[clamp(28px,3.7vw,48px)] font-extrabold leading-[1.18] tracking-[-0.025em] text-ink"
      >
        {storyHeadline.lead} <span className="text-green-bright">{storyHeadline.accentWord}</span>{" "}
        <span className="inline-flex h-[38px] w-[38px] translate-y-1.5 items-center justify-center rounded-full bg-ink align-middle">
          <Target size={20} color="#fff" strokeWidth={2} />
        </span>{" "}
        {storyHeadline.tail.replace(" & commerce.", "")}{" "}
        <span className="inline-flex h-[38px] w-[38px] translate-y-1.5 items-center justify-center rounded-[11px] bg-orange align-middle">
          <Send size={19} color="#fff" strokeWidth={2} />
        </span>{" "}
        &amp; commerce.
      </Reveal>

      <div className="relative mx-auto mt-[50px] h-[330px] w-full max-w-[560px]">
        <div className="absolute left-[-6px] top-[50px] z-30 rounded-[14px_14px_14px_4px] bg-ink px-[13px] py-[7px] text-[13px] font-semibold text-white shadow-[0_12px_26px_-12px_rgba(20,20,18,0.6)] animate-float">
          @alician
        </div>
        <div className="absolute right-[-6px] top-6 z-30 rounded-[14px_14px_4px_14px] bg-blue px-[13px] py-[7px] text-[13px] font-semibold text-white shadow-[0_12px_26px_-12px_rgba(47,107,255,0.6)] animate-float">
          @andrea
        </div>
        <Stagger className="absolute inset-0 flex items-center justify-center">
          {CARDS.map((c, i) =>
            c.center ? (
              <StaggerItem
                key={i}
                className="absolute z-[5] flex h-[262px] w-[200px] items-end overflow-hidden rounded-[20px] border-[5px] border-white shadow-[0_30px_60px_-22px_rgba(20,20,18,0.6)]"
                style={{ background: "#5b8def" }}
              >
                <div
                  className="absolute inset-0"
                  style={{ background: "radial-gradient(circle at 50% 35%,#cfe0ff,#5b8def)" }}
                />
                <div className="relative p-3.5 text-[22px] font-black leading-[0.95] tracking-[-0.02em] text-[#13235e]">
                  FLUFFY
                  <br />
                  WORM
                </div>
              </StaggerItem>
            ) : (
              <StaggerItem
                key={i}
                className="absolute h-[248px] w-[188px] overflow-hidden rounded-[20px] border-[5px] border-white shadow-[0_26px_52px_-22px_rgba(20,20,18,0.55)]"
                style={{ transform: `rotate(${c.rotate}deg) translateX(${c.x}px)`, background: c.bg }}
              >
                <SeededImage seed={c.seed!} luminosity={c.bg !== "#ffffff"} opacity={c.bg === "#ffffff" ? 1 : 0.87} sizes="188px" />
              </StaggerItem>
            )
          )}
        </Stagger>
      </div>

      <div className="mt-10 flex justify-center gap-[7px]">
        <span className="h-2 w-6 rounded-full bg-ink" />
        <span className="h-2 w-2 rounded-full bg-ink/20" />
        <span className="h-2 w-2 rounded-full bg-ink/20" />
      </div>
    </section>
  );
}
