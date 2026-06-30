import { Heart } from "lucide-react";

const TILES = [
  "#e85d7a", "#2f6bff", "#f0c531", "#1f7a44", "#7c5cff",
  "#d98a3c", "#1fa68f", "#171715", "#171715", "#3a4ad6",
  "#caa37a", "#e6522f", "#5b8def", "#2c4a2f", "#e85d7a",
];

export function Spotlight() {
  return (
    <section className="relative z-[5] mx-auto max-w-[1240px] px-6 pb-[100px] pt-10">
      <div className="relative">
        <div className="grid grid-cols-3 gap-3.5 sm:grid-cols-5">
          {TILES.map((bg, i) => (
            <div key={i} className="aspect-[3/4] overflow-hidden rounded-[16px]" style={{ background: bg }}>
              {bg !== "#171715" && (
                /* eslint-disable-next-line @next/next/no-img-element */
                <img
                  src={`https://picsum.photos/seed/ross-g${i + 1}/220/300`}
                  alt=""
                  className="h-full w-full object-cover opacity-[0.85] [mix-blend-mode:luminosity]"
                />
              )}
            </div>
          ))}
        </div>

        {/* centered featured card */}
        <div className="absolute left-1/2 top-1/2 z-20 w-[320px] -translate-x-1/2 -translate-y-1/2">
          <div className="overflow-hidden rounded-[24px] shadow-[0_40px_80px_-30px_rgba(20,20,18,0.6)]">
            <div className="relative h-[340px] bg-orange">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src="https://picsum.photos/seed/ross-spot/360/420"
                alt="Featured artist"
                className="h-full w-full object-cover opacity-[0.78] [mix-blend-mode:luminosity]"
              />
              <div
                className="absolute inset-0"
                style={{ background: "linear-gradient(180deg,rgba(230,82,47,0.1) 30%,rgba(150,40,18,0.9))" }}
              />
              <div className="absolute left-4 top-4 rounded-full bg-white/90 px-3 py-1.5 text-xs font-semibold text-ink">
                @trisha
              </div>
              <button className="absolute right-4 top-4 flex cursor-pointer items-center gap-1.5 rounded-full bg-white/90 px-[13px] py-1.5 text-xs font-semibold text-ink">
                <Heart size={13} fill="#e6522f" color="#e6522f" strokeWidth={2} />
                Like
              </button>
              <div className="absolute inset-x-4 bottom-4 flex items-center gap-[11px]">
                <div className="h-[42px] w-[42px] overflow-hidden rounded-full border-2 border-white/80">
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img src="https://picsum.photos/seed/ross-av/80/80" alt="" className="h-full w-full object-cover" />
                </div>
                <div>
                  <div className="text-base font-bold leading-[1.1] text-white">Trisha Woodward</div>
                  <div className="text-xs text-white/80">from ArtRoss</div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
