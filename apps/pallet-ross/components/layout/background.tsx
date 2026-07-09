const GRAIN =
  "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='180' height='180'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.8' numOctaves='2' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)' opacity='0.06'/%3E%3C/svg%3E\")";

/** Fixed decorative layers: colour glows, dot field, animated grain. */
export function Background() {
  return (
    <>
      <div
        aria-hidden
        className="pointer-events-none fixed inset-0 z-0"
        style={{
          background:
            "radial-gradient(120% 80% at 15% 0%, rgba(70,200,180,0.05), transparent 55%),radial-gradient(110% 70% at 90% 10%, rgba(120,150,255,0.045), transparent 55%)",
        }}
      />
      <div
        aria-hidden
        className="pointer-events-none fixed z-0 opacity-50"
        style={{
          inset: -20,
          backgroundImage:
            "repeating-radial-gradient(circle at 20% 30%, rgba(20,20,18,0.018) 0 1px, transparent 1px 26px),repeating-radial-gradient(circle at 80% 70%, rgba(20,20,18,0.016) 0 1px, transparent 1px 30px)",
        }}
      />
      <div
        aria-hidden
        className="pointer-events-none fixed inset-0 z-[1] opacity-50"
        style={{
          mixBlendMode: "multiply",
          animation: "pr-grain 8s steps(4) infinite",
          backgroundImage: GRAIN,
        }}
      />
    </>
  );
}
