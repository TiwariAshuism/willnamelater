"use client";

import { useState } from "react";
import { motion } from "motion/react";
import { Send, Check } from "lucide-react";
import { cn } from "@/lib/cn";

const FIELD =
  "font-sans text-[15px] text-ink bg-[#f7f7f5] border-[1.5px] border-line rounded-[14px] px-4 py-3.5 outline-none transition focus:border-ink focus:bg-white focus:shadow-[0_0_0_4px_rgba(20,20,18,0.06)]";

export function ContactForm({ roles }: { roles: string[] }) {
  const [role, setRole] = useState(0);
  const [sent, setSent] = useState(false);

  function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (sent) return;
    setSent(true);
    setTimeout(() => setSent(false), 2600);
  }

  return (
    <form
      onSubmit={onSubmit}
      className="rounded-[26px] bg-white p-[34px] shadow-[0_34px_70px_-32px_rgba(20,20,18,0.4)]"
    >
      <div className="flex flex-col gap-[18px]">
        <div className="grid grid-cols-1 gap-[18px] sm:grid-cols-2">
          <label className="flex flex-col gap-2 text-[13px] font-semibold text-muted">
            Full name
            <input type="text" name="name" placeholder="Ada Lovelace" className={FIELD} />
          </label>
          <label className="flex flex-col gap-2 text-[13px] font-semibold text-muted">
            Email
            <input type="email" name="email" placeholder="ada@studio.com" className={FIELD} />
          </label>
        </div>

        <label className="flex flex-col gap-2 text-[13px] font-semibold text-muted">
          I am a…
          <div className="flex gap-2">
            {roles.map((r, i) => (
              <button
                key={r}
                type="button"
                onClick={() => setRole(i)}
                className={cn(
                  "flex-1 cursor-pointer rounded-xl border-[1.5px] py-3 text-sm font-semibold transition-all",
                  role === i
                    ? "border-ink bg-ink text-white"
                    : "border-line bg-[#f7f7f5] text-muted"
                )}
              >
                {r}
              </button>
            ))}
          </div>
        </label>

        <label className="flex flex-col gap-2 text-[13px] font-semibold text-muted">
          Message
          <textarea
            rows={5}
            name="message"
            placeholder="Tell us about your work or what you're looking for…"
            className={cn(FIELD, "resize-y")}
          />
        </label>

        <motion.button
          type="submit"
          animate={sent ? { scale: [0.96, 1] } : {}}
          transition={{ duration: 0.5, ease: [0.16, 1, 0.3, 1] }}
          className="mt-1 flex cursor-pointer items-center justify-center gap-[9px] rounded-full py-4 text-base font-semibold text-white transition"
          style={{ background: sent ? "#1f8a4c" : "#171715" }}
        >
          {sent ? (
            <>
              Message sent <Check size={17} strokeWidth={2.6} />
            </>
          ) : (
            <>
              Send message <Send size={17} strokeWidth={2.2} />
            </>
          )}
        </motion.button>
      </div>
    </form>
  );
}
