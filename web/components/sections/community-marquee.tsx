import { Users } from "lucide-react";
import { home } from "@/lib/content";
import { Reveal, Headline } from "@/components/ui/reveal";

const ROW_A = [
  { bg: "#1f7a44", seed: "ross-q1" },
  { bg: "#e6522f", seed: "ross-q2" },
  { bg: "#2f6bff", seed: "ross-q3" },
  { bg: "#f0c531", seed: "ross-q4" },
  { bg: "#caa37a", seed: "ross-q5" },
  { bg: "#e85d7a", seed: "ross-q6" },
  { bg: "#5b8def", seed: "ross-q7" },
  { bg: "#2c4a2f", seed: "ross-q8" },
];
const ROW_B = [
  { bg: "#d98a3c", seed: "ross-r1" },
  { bg: "#3a4ad6", seed: "ross-r2" },
  { bg: "#1fa68f", seed: "ross-r3" },
  { bg: "#f0c531", seed: "ross-r4" },
  { bg: "#e85d7a", seed: "ross-r5" },
  { bg: "#171715", seed: "ross-r6" },
  { bg: "#caa37a", seed: "ross-r7" },
  { bg: "#2f6bff", seed: "ross-r8" },
];

const MASK = "linear-gradient(90deg,transparent,#000 8%,#000 92%,transparent)";

function Row({
  tiles,
  animation,
}: {
  tiles: { bg: string; seed: string }[];
  animation: string;
}) {
  // four copies → seamless loop with translateX(-50%)
  const loop = [...tiles, ...tiles, ...tiles, ...tiles];
  return (
    <div className="overflow-hidden" style={{ maskImage: MASK, WebkitMaskImage: MASK }}>
      <div className="marquee-track flex gap-4" style={{ animation }}>
        {loop.map((t, i) => (
          <div
            key={i}
            className="h-[118px] w-[118px] flex-none overflow-hidden rounded-[18px]"
            style={{ background: t.bg }}
          >
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={`https://picsum.photos/seed/${t.seed}/160/160`}
              alt=""
              className="h-full w-full object-cover opacity-90 [mix-blend-mode:luminosity]"
            />
          </div>
        ))}
      </div>
    </div>
  );
}

export function CommunityMarquee() {
  const { community } = home;
  return (
    <section className="relative z-[5] overflow-hidden py-20">
      <div className="relative flex flex-col gap-4">
        <Row tiles={ROW_A} animation="pr-marqL 28s linear infinite" />
        <Row tiles={ROW_B} animation="pr-marqR 28s linear infinite" />

        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center text-center">
          <div className="rounded-[28px] bg-canvas/[0.86] p-[30px_46px] shadow-[0_30px_60px_-30px_rgba(20,20,18,0.4)] backdrop-blur-md">
            <div className="mx-auto flex h-[46px] w-[46px] items-center justify-center rounded-[14px] bg-ink">
              <Users size={22} color="#fff" strokeWidth={2} />
            </div>
            <h2 className="mx-auto mt-4 max-w-[14ch] text-[clamp(28px,3.6vw,46px)] font-extrabold tracking-[-0.03em]">
              <Headline parts={[{ text: community.title }]} accentColor="#171715" />
            </h2>
            <Reveal as="p" className="mx-auto mt-3 max-w-[40ch] text-[15px] leading-[1.5] text-muted">
              {community.body}
            </Reveal>
          </div>
        </div>
      </div>
    </section>
  );
}
