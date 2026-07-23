import Link from "next/link";
import { site } from "@/lib/content";
import { Reveal } from "@/components/ui/reveal";
import { LogoMark, Wordmark } from "@/components/ui/logo";
import { SocialIcon } from "@/components/ui/social-icon";

const BADGE_TONE = {
  orange: "text-orange bg-orange/10",
  yellow: "text-[#9a7b00] bg-yellow/25",
} as const;

export function Footer() {
  const { footer, socials } = site;
  return (
    <footer className="relative z-[5] mx-auto max-w-[1240px] px-6 pb-[60px] pt-5">
      <Reveal className="grid grid-cols-1 gap-10 rounded-[30px] bg-[#e7e7e3] p-[48px_44px] md:grid-cols-[1.4fr_1fr_1fr_1fr]">
        <div>
          <div className="flex gap-1.5">
            <span className="h-[9px] w-[9px] rounded-full bg-ink" />
            <span className="h-[9px] w-[9px] rounded-full bg-ink/30" />
            <span className="h-[9px] w-[9px] rounded-full bg-ink/30" />
          </div>
          <h2 className="mt-5 max-w-[11ch] text-[34px] font-extrabold leading-[1.05] tracking-[-0.03em]">
            {footer.headline}
          </h2>
          <p className="mt-3.5 max-w-[36ch] text-[15px] leading-[1.55] text-muted">{footer.blurb}</p>
          <div className="mt-6 flex gap-[11px]">
            {socials.map((s) => (
              <a
                key={s.label}
                href={s.href}
                aria-label={s.label}
                className="flex h-[42px] w-[42px] items-center justify-center rounded-full bg-white text-ink shadow-[0_8px_18px_-10px_rgba(20,20,18,0.3)]"
              >
                <SocialIcon name={s.icon} size={17} />
              </a>
            ))}
          </div>
        </div>

        {footer.columns.map((col) => (
          <div key={col.title}>
            <div className="text-[13px] font-bold tracking-[0.04em] text-faint">{col.title}</div>
            <div className="mt-[18px] flex flex-col gap-[13px] text-[15px] text-[#3a3a36]">
              {col.links.map((l) => (
                <Link
                  key={l.label}
                  href={l.href}
                  className="flex items-center gap-2 text-[#3a3a36] no-underline hover:text-ink"
                >
                  {l.label}
                  {l.badge && (
                    <span
                      className={`rounded-full px-[7px] py-0.5 text-[10px] font-bold ${BADGE_TONE[l.badge.tone]}`}
                    >
                      {l.badge.text}
                    </span>
                  )}
                </Link>
              ))}
            </div>
          </div>
        ))}
      </Reveal>

      <div className="mt-[22px] flex flex-wrap items-center justify-between gap-3 px-2">
        <div className="flex items-center gap-[9px]">
          <LogoMark size={30} radius={10} iconSize={15} glow={false} />
          <Wordmark name={site.brand.name} size={15} />
        </div>
        <div className="text-sm text-faint">{footer.copyright}</div>
      </div>
    </footer>
  );
}
