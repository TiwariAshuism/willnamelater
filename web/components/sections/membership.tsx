import { Wallet } from "lucide-react";
import { home } from "@/lib/content";
import { Reveal, Headline, Stagger, StaggerItem } from "@/components/ui/reveal";

export function Membership() {
  const { membership } = home;
  return (
    <section className="relative z-[5] mx-auto max-w-[1100px] px-6 py-[90px] text-center">
      <div className="mx-auto flex h-[46px] w-[46px] items-center justify-center rounded-[14px] bg-white shadow-[0_10px_24px_-10px_rgba(20,20,18,0.28)]">
        <Wallet size={22} color="#171715" strokeWidth={2} />
      </div>
      <h2 className="mt-5 text-[clamp(32px,4.2vw,52px)] font-extrabold tracking-[-0.03em]">
        <Headline parts={[{ text: membership.title }]} accentColor="#171715" />
      </h2>
      <Reveal as="p" className="mx-auto mt-3.5 max-w-[44ch] text-base leading-[1.55] text-muted">
        {membership.body}
      </Reveal>

      <Stagger className="mt-12 grid grid-cols-1 items-center gap-[18px] text-left md:grid-cols-3">
        {membership.plans.map((plan) => (
          <StaggerItem
            key={plan.cadence}
            className={
              plan.featured
                ? "relative rounded-[26px] bg-orange p-[34px_30px] text-white shadow-[0_34px_70px_-26px_rgba(230,82,47,0.6)] md:scale-[1.06]"
                : "rounded-[24px] bg-white p-7 shadow-[0_24px_50px_-26px_rgba(20,20,18,0.3)]"
            }
          >
            {plan.badge && (
              <div className="absolute right-6 top-6 rounded-full bg-white px-[13px] py-1.5 text-xs font-bold text-orange">
                {plan.badge}
              </div>
            )}
            <div
              className="text-sm font-semibold"
              style={{ color: plan.featured ? "rgba(255,255,255,0.8)" : "var(--color-faint)" }}
            >
              {plan.cadence}
            </div>
            <div
              className="mt-2.5 font-extrabold tracking-[-0.03em]"
              style={{ fontSize: plan.featured ? 50 : 44 }}
            >
              {plan.price}
            </div>
            <div
              className="text-[13px]"
              style={{ color: plan.featured ? "rgba(255,255,255,0.78)" : "var(--color-faint)" }}
            >
              {plan.note}
            </div>
            {plan.discount && (
              <div className="mt-2.5 inline-block rounded-full bg-green-bright/10 px-[11px] py-[5px] text-xs font-bold text-green-bright">
                {plan.discount}
              </div>
            )}
            <button
              className={
                plan.featured
                  ? "mt-[22px] w-full cursor-pointer rounded-full bg-white py-[15px] text-[15px] font-bold text-orange transition hover:scale-[1.02]"
                  : "mt-[18px] w-full cursor-pointer rounded-full border-[1.5px] border-ink/15 bg-white py-3.5 text-[15px] font-semibold text-ink transition hover:scale-[1.02]"
              }
            >
              Choose plan
            </button>
          </StaggerItem>
        ))}
      </Stagger>
    </section>
  );
}
