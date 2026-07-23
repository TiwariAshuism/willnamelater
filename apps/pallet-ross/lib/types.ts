// Shared content types — every JSON file under /data is typed against these.

export interface NavDropdownItem {
  title: string;
  desc?: string;
  icon: string; // lucide icon name
  accent: string; // hex
  bg: string; // hex tint
  badge?: string;
}

export interface NavItem {
  label: string;
  href: string;
  dot?: boolean; // small accent dot (e.g. "Create strategy")
  dropdown?: NavDropdownItem[];
}

export interface FooterLink {
  label: string;
  href: string;
  badge?: { text: string; tone: "orange" | "yellow" };
}

export interface FooterColumn {
  title: string;
  links: FooterLink[];
}

export interface SocialLink {
  label: string;
  href: string;
  icon: string; // custom key resolved in <SocialIcon>
}

export interface SiteData {
  brand: { name: string };
  nav: NavItem[];
  footer: {
    headline: string;
    blurb: string;
    columns: FooterColumn[];
    copyright: string;
  };
  socials: SocialLink[];
}

export interface CtaButton {
  label: string;
  href?: string;
}

export interface Step {
  index: string;
  icon: string;
  accent: string;
  bg: string;
  title: string;
  body: string;
  dark?: boolean;
}

export interface Stat {
  value: number;
  decimals?: number;
  prefix?: string;
  suffix?: string;
  label: string;
  accent?: string;
}

export interface PricingPlan {
  cadence: string;
  price: string;
  note: string;
  badge?: string;
  discount?: string;
  featured?: boolean;
}

export interface MarketItem {
  title: string;
  desc: string;
  bg: string;
  seed?: string;
  pattern?: "dots" | "spectral";
}

export interface BentoReelItem {
  text: string;
}

export interface HomeData {
  hero: {
    headlineLead: string;
    headlineAccent: string;
    body: string;
    primaryCta: CtaButton;
    secondaryCta: CtaButton;
    fan: { bg: string; seed?: string; label?: string }[];
    bubbles: { text: string; bg: string; side: "left" | "right" }[];
  };
  ecommerce: {
    eyebrow: string;
    headParts: { text: string; accent?: boolean }[];
    body: string;
    primaryCta: CtaButton;
    secondaryCta: CtaButton;
  };
  howItWorks: { eyebrow: string; title: string; body: string; steps: Step[] };
  classSection: { authors: string[]; eyebrowPrefix: string; title: string; bubble: string };
  trusted: { title: string; titleStrong: string; titleTail: string; body: string; logos: string[] };
  stats: Stat[];
  storyHeadline: { lead: string; accentWord: string; tail: string };
  vision: {
    title: string;
    body: string;
    tabs: string[];
  };
  community: { title: string; body: string };
  bento: {
    eyebrowParts: { text: string; accent?: boolean }[];
    title: string;
    reel: BentoReelItem[];
  };
  marketplace: {
    eyebrowParts: { text: string; accent?: boolean }[];
    title: string;
    body: string;
    items: MarketItem[];
  };
  integrations: { eyebrow: string; title: string };
  membership: { title: string; body: string; plans: PricingPlan[] };
  yellowBand: { words: string[] };
  featureCards: {
    title: string;
    body: string;
    cta: string;
    tone: "pink" | "light";
    seed?: string;
  }[];
}

export interface ContactInfo {
  icon: string;
  accent?: string;
  bg?: string;
  label: string;
  value: string;
  dark?: boolean;
}

export interface ContactData {
  eyebrow: string;
  headlineLead: string;
  headlineAccent: string;
  body: string;
  info: ContactInfo[];
  roles: string[];
  email: string;
}
