import { home } from "@/lib/content";
import { Reveal, Headline, Stagger, StaggerItem } from "@/components/ui/reveal";
import { Icon } from "@/components/ui/icon";

export function HowItWorks() {
  const { howItWorks } = home;
  return (
    <section className="relative z-[5] mx-auto max-w-[1240px] px-6 py-20">
      <div className="mb-12 text-center">
        <Reveal className="inline-flex text-xs font-bold tracking-[0.16em] text-faint">
          {howItWorks.eyebrow}
        </Reveal>
        <h2 className="mx-auto mt-3.5 max-w-[16ch] text-[clamp(32px,4.2vw,54px)] font-extrabold tracking-[-0.03em]">
          <Headline parts={[{ text: howItWorks.title }]} />
        </h2>
        <Reveal as="p" className="mx-auto mt-4 max-w-[50ch] text-base leading-[1.55] text-muted">
          {howItWorks.body}
        </Reveal>
      </div>

      <Stagger className="grid grid-cols-1 gap-[18px] md:grid-cols-3">
        {howItWorks.steps.map((step) => (
          <StaggerItem
            key={step.index}
            className={`rounded-[24px] p-[30px] shadow-[0_24px_50px_-28px_rgba(20,20,18,0.3)] ${
              step.dark ? "bg-ink text-white" : "bg-white"
            }`}
          >
            <div className="flex items-center justify-between">
              <div
                className="flex h-[52px] w-[52px] items-center justify-center rounded-[15px]"
                style={{ background: step.bg }}
              >
                <Icon name={step.icon} size={24} color={step.accent} />
              </div>
              <span
                className="text-[42px] font-extrabold tracking-[-0.04em]"
                style={{ color: step.dark ? "rgba(255,255,255,0.15)" : "#ececea" }}
              >
                {step.index}
              </span>
            </div>
            <h3 className="mt-[22px] text-[21px] font-extrabold tracking-[-0.02em]">{step.title}</h3>
            <p
              className="mt-2.5 text-sm leading-[1.55]"
              style={{ color: step.dark ? "rgba(255,255,255,0.72)" : "var(--color-muted)" }}
            >
              {step.body}
            </p>
          </StaggerItem>
        ))}
      </Stagger>
    </section>
  );
}
