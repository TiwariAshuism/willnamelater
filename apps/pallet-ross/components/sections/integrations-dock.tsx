import { LayoutGrid, Plus, PlusCircle, Send, MessageSquare, Sparkles, Settings } from "lucide-react";
import { home } from "@/lib/content";
import { Reveal, Headline } from "@/components/ui/reveal";

const ITEMS = [
  { icon: LayoutGrid, bg: "#2f6bff", size: 56, color: "#fff" },
  { icon: Plus, bg: "#171715", size: 56, color: "#fff" },
  { icon: PlusCircle, bg: "#e85d7a", size: 56, color: "#fff" },
  { icon: Send, bg: "linear-gradient(140deg,#ff8a4c,#e6522f)", size: 68, color: "#fff", lift: true },
  { icon: MessageSquare, bg: "#1fa68f", size: 56, color: "#fff" },
  { icon: Sparkles, bg: "#f0c531", size: 56, color: "#171715" },
  { icon: Settings, bg: "#7c5cff", size: 56, color: "#fff" },
];

export function IntegrationsDock() {
  const { integrations } = home;
  return (
    <section className="relative z-[5] mx-auto max-w-[1240px] px-6 pb-[100px] pt-[50px] text-center">
      <Reveal className="text-xs font-bold tracking-[0.16em] text-faint">{integrations.eyebrow}</Reveal>
      <h2 className="mx-auto mb-10 mt-3.5 max-w-[16ch] text-[clamp(26px,3.4vw,40px)] font-extrabold tracking-[-0.03em]">
        <Headline parts={[{ text: integrations.title }]} accentColor="#171715" />
      </h2>
      <div className="flex justify-center">
        <div className="flex items-end gap-3.5 rounded-[26px] border border-ink/5 bg-white/70 p-[14px_18px] shadow-[0_24px_50px_-22px_rgba(20,20,18,0.35)] backdrop-blur-md">
          {ITEMS.map((it, i) => {
            const Ico = it.icon;
            return (
              <div
                key={i}
                className="flex origin-bottom items-center justify-center rounded-[16px] transition-transform duration-200 hover:-translate-y-1.5 hover:scale-125"
                style={{
                  width: it.size,
                  height: it.size,
                  borderRadius: it.lift ? 20 : 16,
                  background: it.bg,
                  boxShadow: "0 10px 22px -10px rgba(20,20,18,0.5)",
                }}
              >
                <Ico size={it.size > 56 ? 32 : 26} color={it.color} strokeWidth={2} />
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}
