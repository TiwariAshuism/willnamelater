import { Box, Gem, Diamond, Send, Anchor, Navigation, Infinity as Inf } from "lucide-react";
import { home } from "@/lib/content";

const ICONS = [Box, Gem, Diamond, Send, Anchor, Navigation, Inf];
const IMGS = ["ross-y1", "ross-y2", "ross-y3", "ross-y4"];

function MarqueeContent() {
  const { words } = home.yellowBand;
  return (
    <>
      {words.map((w, i) => (
        <span key={i} className="flex items-center gap-[30px]">
          <span>{w}</span>
          <span
            className="h-[62px] w-[62px] flex-none overflow-hidden"
            style={{ borderRadius: i % 2 === 0 ? 16 : "50%" }}
          >
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={`https://picsum.photos/seed/${IMGS[i % IMGS.length]}/120/120`} alt="" className="h-full w-full object-cover" />
          </span>
        </span>
      ))}
    </>
  );
}

export function YellowBand() {
  return (
    <section className="relative z-[5]">
      <div className="relative flex min-h-[520px] w-full flex-col items-center justify-center gap-[clamp(34px,5vh,64px)] overflow-hidden bg-[#d2e84a] py-16">
        <div
          aria-hidden
          className="pointer-events-none absolute left-1/2 top-1/2 h-[860px] w-[860px] -translate-x-1/2 -translate-y-1/2 rounded-full"
          style={{ background: "repeating-radial-gradient(circle,transparent 0 38px,rgba(20,20,18,0.05) 38px 39px)" }}
        />
        <div className="absolute left-6 top-5 text-xs font-bold tracking-[0.18em] text-ink/50">2025 POSTER</div>
        <div className="absolute right-6 top-5 text-xs font-bold tracking-[0.18em] text-ink/50">PALLET ROSS</div>

        <div className="relative w-full overflow-hidden">
          <div
            className="flex w-max items-center gap-[30px] whitespace-nowrap text-[clamp(40px,6vw,86px)] font-black tracking-[-0.03em] text-ink"
            style={{ animation: "pr-marqL 30s linear infinite" }}
          >
            <MarqueeContent />
            <MarqueeContent />
          </div>
        </div>

        <div className="relative z-[2] flex flex-wrap justify-center gap-[clamp(12px,1.6vw,20px)]">
          {ICONS.map((Ico, i) => (
            <div
              key={i}
              className="flex h-[72px] w-[72px] items-center justify-center rounded-full border-[1.5px] border-ink/10 bg-white shadow-[0_14px_30px_-14px_rgba(20,20,18,0.3)]"
            >
              <Ico size={28} color="#171715" strokeWidth={1.8} />
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
