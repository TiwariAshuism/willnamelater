// Brand glyphs that aren't in lucide — kept as small inline paths.

const PATHS: Record<string, React.ReactNode> = {
  x: <path d="M18.9 2H22l-7.5 8.6L23 22h-6.8l-5.3-7-6.1 7H1.6l8-9.2L1 2h7l4.8 6.4L18.9 2zm-1.2 18h1.9L7.4 4H5.4l12.3 16z" />,
  behance: (
    <path d="M9.6 5.5c.7 0 1.6.2 2.1.7.5.4.7 1 .7 1.7 0 .8-.3 1.4-.9 1.8.8.3 1.3 1.1 1.3 2.1 0 .8-.3 1.4-.8 1.9-.6.4-1.4.6-2.4.6H5V5.5h4.6zm-.3 3.5c.4 0 .7-.1.9-.3.2-.1.3-.4.3-.7s-.1-.5-.3-.7c-.2-.1-.5-.2-.9-.2H6.8V9h2.5zm.1 3.6c.5 0 .8-.1 1-.3.2-.2.4-.4.4-.8 0-.3-.1-.6-.4-.8-.2-.2-.6-.3-1-.3H6.8v2.2h2.7zM19 9.3h-4.8v1h4.8v-1zm-.4 3.1c-.3.9-1.2 1.5-2.4 1.5-1.7 0-2.8-1.1-2.8-2.9s1.1-3 2.7-3c1.7 0 2.7 1.2 2.7 3.1v.3h-4c.1.8.6 1.3 1.4 1.3.6 0 1-.2 1.2-.6h1.2zm-3.9-1.9h2.7c-.1-.7-.5-1.1-1.3-1.1-.7 0-1.2.4-1.4 1.1z" />
  ),
};

const STROKE: Record<string, React.ReactNode> = {
  instagram: (
    <>
      <rect x="2" y="2" width="20" height="20" rx="5.5" />
      <circle cx="12" cy="12" r="4.2" />
      <circle cx="17.4" cy="6.6" r="1.2" fill="currentColor" stroke="none" />
    </>
  ),
  dribbble: (
    <>
      <circle cx="12" cy="12" r="10" />
      <path d="M2.5 9.5c6 1 13 1 19 0M5 4.5c3.5 4 5 8 6 15M14.5 3c-3 5-7 9-12 11" />
    </>
  ),
};

export function SocialIcon({ name, size = 18 }: { name: string; size?: number }) {
  if (name in STROKE) {
    return (
      <svg
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth={2}
      >
        {STROKE[name]}
      </svg>
    );
  }
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor">
      {PATHS[name] ?? PATHS.x}
    </svg>
  );
}
