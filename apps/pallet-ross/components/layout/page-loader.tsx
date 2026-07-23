"use client";

import { useEffect, useState } from "react";
import { motion, AnimatePresence } from "motion/react";
import { LogoMark, Wordmark } from "@/components/ui/logo";

/** Branded intro loader — fills a progress bar, then lifts away. */
export function PageLoader({ caption = "CURATING THE GALLERY…" }: { caption?: string }) {
  const [done, setDone] = useState(false);

  useEffect(() => {
    const t = setTimeout(() => setDone(true), 1450);
    return () => clearTimeout(t);
  }, []);

  return (
    <AnimatePresence>
      {!done && (
        <motion.div
          className="fixed inset-0 z-[9999] flex flex-col items-center justify-center gap-[26px] bg-canvas"
          exit={{ opacity: 0, y: -26 }}
          transition={{ duration: 0.6, ease: [0.16, 1, 0.3, 1] }}
        >
          <div className="flex items-center gap-[13px]">
            <span className="relative">
              <span
                className="absolute"
                style={{
                  inset: -7,
                  borderRadius: 22,
                  border: "2.5px solid rgba(31,166,143,0.25)",
                  borderTopColor: "#1fa68f",
                  animation: "pr-spin 0.9s linear infinite",
                }}
              />
              <LogoMark size={54} radius={17} iconSize={27} />
            </span>
            <Wordmark name="Pallet Ross" size={26} />
          </div>
          <div className="h-1 w-[210px] overflow-hidden rounded-full bg-ink/10">
            <motion.div
              className="h-full rounded-full bg-ink"
              initial={{ width: "6%" }}
              animate={{ width: "100%" }}
              transition={{ duration: 1.15, ease: [0.16, 1, 0.3, 1] }}
            />
          </div>
          <div className="text-xs font-semibold tracking-[0.16em] text-faint">{caption}</div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
