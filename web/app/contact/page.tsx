import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft } from "lucide-react";
import { contact, site } from "@/lib/content";
import { Background } from "@/components/layout/background";
import { PageLoader } from "@/components/layout/page-loader";
import { LogoMark, Wordmark } from "@/components/ui/logo";
import { Icon } from "@/components/ui/icon";
import { SocialIcon } from "@/components/ui/social-icon";
import { Reveal, Headline, Stagger, StaggerItem } from "@/components/ui/reveal";
import { ContactForm } from "@/components/contact/contact-form";

export const metadata: Metadata = {
  title: "Contact — Pallet Ross",
  description:
    "Whether you're an artist ready to sell or a collector hunting your next piece — get in touch with Pallet Ross.",
};

export default function ContactPage() {
  return (
    <div className="relative min-h-screen w-full overflow-x-clip bg-canvas">
      <PageLoader caption="OPENING THE STUDIO DOOR…" />
      <Background />

      <header className="sticky top-0 z-[60] flex w-full justify-center px-6 py-[18px]">
        <div className="flex w-full max-w-[1180px] items-center justify-between">
          <Link href="/" className="flex items-center gap-[11px] no-underline">
            <LogoMark />
            <Wordmark name={site.brand.name} />
          </Link>
          <Link
            href="/"
            className="flex items-center gap-2 rounded-full bg-white px-[18px] py-[11px] text-sm font-semibold text-ink no-underline shadow-[0_8px_20px_-10px_rgba(20,20,18,0.3)] transition hover:-translate-y-px"
          >
            <ArrowLeft size={16} strokeWidth={2.2} />
            Back to home
          </Link>
        </div>
      </header>

      <section className="relative z-[5] mx-auto max-w-[1180px] px-6 pb-[90px] pt-10">
        <div className="mx-auto mb-14 max-w-[760px] text-center">
          <Reveal className="inline-flex items-center gap-2 rounded-full bg-orange/10 px-3.5 py-[7px] text-xs font-bold tracking-[0.16em] text-orange">
            {contact.eyebrow}
          </Reveal>
          <h1 className="mt-[22px] text-[clamp(40px,6vw,76px)] font-extrabold leading-[1.03] tracking-[-0.035em]">
            <Headline
              parts={[{ text: contact.headlineLead }, { text: contact.headlineAccent, accent: true }]}
            />
          </h1>
          <Reveal as="p" className="mx-auto mt-[22px] max-w-[48ch] text-[17px] leading-[1.55] text-muted">
            {contact.body}
          </Reveal>
        </div>

        <div className="grid grid-cols-1 items-stretch gap-6 md:grid-cols-[0.85fr_1.15fr]">
          {/* info column */}
          <Stagger className="flex flex-col gap-3.5">
            {contact.info.map((card) => (
              <StaggerItem
                key={card.label}
                className={
                  card.dark
                    ? "flex min-h-[200px] flex-1 flex-col justify-between rounded-[24px] bg-ink p-[26px] text-white"
                    : "flex items-center gap-4 rounded-[24px] bg-white p-6 shadow-[0_22px_46px_-26px_rgba(20,20,18,0.3)]"
                }
              >
                {card.dark ? (
                  <>
                    <div className="flex h-[46px] w-[46px] items-center justify-center rounded-[14px] bg-white/10">
                      <Icon name={card.icon} size={22} color="#fff" />
                    </div>
                    <div>
                      <div className="text-[13px] text-white/60">{card.label}</div>
                      <div className="mt-1 text-[22px] font-bold tracking-[-0.02em]">{card.value}</div>
                    </div>
                  </>
                ) : (
                  <>
                    <div
                      className="flex h-[46px] w-[46px] flex-none items-center justify-center rounded-[14px]"
                      style={{ background: card.bg }}
                    >
                      <Icon name={card.icon} size={22} color={card.accent} />
                    </div>
                    <div>
                      <div className="text-[13px] text-faint">{card.label}</div>
                      <div className="text-base font-bold tracking-[-0.01em]">{card.value}</div>
                    </div>
                  </>
                )}
              </StaggerItem>
            ))}

            <StaggerItem className="rounded-[24px] bg-white p-6 shadow-[0_22px_46px_-26px_rgba(20,20,18,0.3)]">
              <div className="mb-3.5 text-[13px] text-faint">Follow the gallery</div>
              <div className="flex gap-[11px]">
                {site.socials.slice(0, 3).map((s) => (
                  <a
                    key={s.label}
                    href={s.href}
                    aria-label={s.label}
                    className="flex h-[42px] w-[42px] items-center justify-center rounded-full bg-[#f4f4f2] text-ink transition hover:bg-ink hover:text-white"
                  >
                    <SocialIcon name={s.icon} size={17} />
                  </a>
                ))}
              </div>
            </StaggerItem>
          </Stagger>

          {/* form */}
          <Reveal>
            <ContactForm roles={contact.roles} />
          </Reveal>
        </div>
      </section>
    </div>
  );
}
