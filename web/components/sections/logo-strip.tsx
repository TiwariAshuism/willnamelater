import { home } from "@/lib/content";
import { Reveal, Headline, Stagger, StaggerItem } from "@/components/ui/reveal";

export function LogoStrip() {
  const { trusted } = home;
  return (
    <section className="relative z-[5] mx-auto max-w-[1240px] px-6 pb-20 pt-[50px] text-center">
      <h2 className="m-0 text-[clamp(30px,3.8vw,46px)] font-extrabold tracking-[-0.03em]">
        <Headline
          parts={[
            { text: trusted.title },
            { text: trusted.titleStrong },
            { text: trusted.titleTail },
          ]}
          accentColor="#171715"
        />
      </h2>
      <Reveal as="p" className="mx-auto mt-4 max-w-[44ch] text-base leading-[1.55] text-muted">
        {trusted.body}
      </Reveal>
      <Stagger className="mt-[46px] flex flex-wrap items-center justify-between gap-x-10 gap-y-[26px]">
        {trusted.logos.map((logo) => (
          <StaggerItem key={logo}>
            <span className="text-2xl font-bold tracking-[-0.02em] text-ink opacity-[0.42]">{logo}</span>
          </StaggerItem>
        ))}
      </Stagger>
    </section>
  );
}
