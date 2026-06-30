import { Brush, Printer } from "lucide-react";
import { home } from "@/lib/content";
import { Reveal } from "@/components/ui/reveal";

export function FeatureCards() {
  const [pink, light] = home.featureCards;
  return (
    <section className="relative z-[5] mx-auto grid max-w-[1240px] grid-cols-1 gap-5 px-6 pb-[70px] pt-[60px] md:grid-cols-2">
      {/* pink image card */}
      <Reveal className="relative flex min-h-[440px] flex-col justify-end overflow-hidden rounded-[26px] bg-[#d6457f] shadow-[0_30px_64px_-28px_rgba(214,69,127,0.5)]">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          src={`https://picsum.photos/seed/${pink.seed}/700/620`}
          alt=""
          className="absolute inset-0 h-full w-full object-cover opacity-[0.72] [mix-blend-mode:luminosity]"
        />
        <div
          className="absolute inset-0"
          style={{ background: "linear-gradient(180deg,rgba(214,69,127,0.15) 30%,rgba(120,28,72,0.92))" }}
        />
        <div className="absolute left-6 top-6 flex h-[42px] w-[42px] items-center justify-center rounded-xl bg-white/90">
          <Brush size={20} color="#d6457f" strokeWidth={2} />
        </div>
        <div className="relative p-[30px]">
          <h3 className="m-0 text-[30px] font-extrabold tracking-[-0.02em] text-white">{pink.title}</h3>
          <p className="mt-2.5 max-w-[34ch] text-[15px] leading-[1.5] text-white/80">{pink.body}</p>
          <button className="mt-5 cursor-pointer rounded-full bg-white px-6 py-[13px] text-sm font-semibold text-ink transition hover:scale-[1.03]">
            {pink.cta}
          </button>
        </div>
      </Reveal>

      {/* light gradient card */}
      <Reveal className="relative flex min-h-[440px] flex-col overflow-hidden rounded-[26px] bg-white shadow-[0_30px_64px_-28px_rgba(20,20,18,0.3)]">
        <div
          className="relative m-3.5 mb-0 min-h-[230px] flex-1 overflow-hidden rounded-[18px]"
          style={{ background: "conic-gradient(from 120deg,#ff5d73,#ffb347,#3fd2c7,#7c5cff,#ff5d73)" }}
        >
          <div
            className="absolute inset-0"
            style={{ background: "radial-gradient(circle at 50% 45%,rgba(255,255,255,0.45),transparent 55%)" }}
          />
          <div className="absolute right-[18px] top-[18px] flex h-[42px] w-[42px] items-center justify-center rounded-xl bg-white/90">
            <Printer size={20} color="#171715" strokeWidth={2} />
          </div>
        </div>
        <div className="p-[24px_28px_30px]">
          <h3 className="m-0 text-[30px] font-extrabold tracking-[-0.02em]">{light.title}</h3>
          <p className="mt-2.5 max-w-[38ch] text-[15px] leading-[1.5] text-muted">{light.body}</p>
          <button className="mt-[18px] cursor-pointer rounded-full bg-ink px-6 py-[13px] text-sm font-semibold text-white transition hover:scale-[1.03]">
            {light.cta}
          </button>
        </div>
      </Reveal>
    </section>
  );
}
