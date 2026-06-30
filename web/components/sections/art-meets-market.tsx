import { TrendingUp, ArrowUpRight } from "lucide-react";
import { Stagger, StaggerItem } from "@/components/ui/reveal";
import { SeededImage } from "@/components/ui/seeded-image";

const TILES = [
  { bg: "#1f7a44", label: "STAFF", labelPos: "bottom", seed: null },
  { bg: "#d98a3c", label: "le FLEUR", labelPos: "bl-italic", seed: "ross-m3" },
  { bg: "#2c4a2f", label: "THE GREEN KNIGHT", labelPos: "bottom-c", seed: "ross-m4" },
  { bg: "#e85d7a", label: "GLIMMER", labelPos: "top-c", seed: "ross-m5" },
  { bg: "#f0c531", label: null, labelPos: null, seed: "ross-m7" },
];

export function ArtMeetsMarket() {
  return (
    <section className="relative z-[5] mx-auto max-w-[1240px] px-6 pb-[90px] pt-[50px]">
      <div className="relative">
        <div className="absolute right-[120px] top-[-16px] z-30 rounded-[14px_14px_14px_4px] bg-orange px-[13px] py-[7px] text-[13px] font-semibold text-white shadow-[0_12px_26px_-12px_rgba(230,82,47,0.6)] animate-float">
          @simmon
        </div>
        <Stagger className="grid grid-cols-2 items-stretch gap-3.5 md:grid-cols-7">
          <StaggerItem className="col-span-2 flex flex-col justify-between rounded-[20px] bg-white p-[22px] shadow-[0_22px_46px_-24px_rgba(20,20,18,0.32)]">
            <div className="flex items-start justify-between">
              <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-canvas">
                <TrendingUp size={20} color="#171715" strokeWidth={2} />
              </div>
              <div className="flex h-8 w-8 items-center justify-center rounded-[9px] bg-orange">
                <ArrowUpRight size={15} color="#fff" strokeWidth={2.5} />
              </div>
            </div>
            <div>
              <h3 className="m-0 text-[23px] font-extrabold leading-[1.05] tracking-[-0.02em]">
                Where Art Meets Market
              </h3>
              <div className="mt-2 text-[13px] font-bold text-green-bright">
                +4.60% <span className="font-medium text-faint">avg. resale uplift</span>
              </div>
              <p className="mt-2.5 text-[13.5px] leading-[1.5] text-muted">
                Artists showcase the work; buyers find unique, inspiring pieces.
              </p>
            </div>
          </StaggerItem>

          {TILES.map((t, i) => (
            <StaggerItem
              key={i}
              className="relative aspect-[3/4] overflow-hidden rounded-[18px] shadow-[0_16px_34px_-18px_rgba(20,20,18,0.4)]"
              style={{ background: t.bg }}
            >
              {t.seed && <SeededImage seed={t.seed} luminosity opacity={0.85} sizes="160px" />}
              {t.label && t.labelPos === "bottom" && (
                <span className="absolute inset-x-0 bottom-3 text-center text-[15px] font-black tracking-[0.05em] text-[#eafff2]">
                  {t.label}
                </span>
              )}
              {t.label && t.labelPos === "bl-italic" && (
                <span className="absolute bottom-2.5 left-2.5 text-sm font-extrabold italic text-white">
                  {t.label}
                </span>
              )}
              {t.label && t.labelPos === "bottom-c" && (
                <span className="absolute inset-x-0 bottom-3.5 text-center text-[11px] font-extrabold tracking-[0.04em] text-[#d8e8c8]">
                  {t.label}
                </span>
              )}
              {t.label && t.labelPos === "top-c" && (
                <span className="absolute inset-x-0 top-3.5 text-center text-[15px] font-black italic tracking-[0.06em] text-white">
                  {t.label}
                </span>
              )}
            </StaggerItem>
          ))}
        </Stagger>
      </div>
    </section>
  );
}
