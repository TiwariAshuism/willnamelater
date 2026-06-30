import Link from "next/link";
import { ChevronDown, User, Sun } from "lucide-react";
import { site } from "@/lib/content";
import { Icon } from "@/components/ui/icon";
import { LogoMark, Wordmark } from "@/components/ui/logo";
import type { NavItem } from "@/lib/types";

function Dropdown({ item }: { item: NavItem }) {
  return (
    <div className="absolute left-0 top-full z-[80] hidden pt-3 group-hover:block">
      <div className="w-[248px] rounded-[18px] border border-ink/5 bg-white p-[9px] shadow-[0_24px_50px_-20px_rgba(20,20,18,0.35)]">
        {item.dropdown!.map((d) => (
          <Link
            key={d.title}
            href="#"
            className="flex items-center gap-[11px] rounded-xl p-[10px] no-underline transition-colors hover:bg-[#f4f4f2]"
          >
            <span
              className="flex h-[34px] w-[34px] flex-none items-center justify-center rounded-[10px]"
              style={{ background: d.bg }}
            >
              <Icon name={d.icon} size={17} color={d.accent} />
            </span>
            <span>
              <span className="flex items-center gap-1.5 font-semibold text-ink">
                {d.title}
                {d.badge && (
                  <span className="rounded-full bg-orange/10 px-1.5 py-px text-[9px] font-bold text-orange">
                    {d.badge}
                  </span>
                )}
              </span>
              {d.desc && <span className="block text-xs text-faint">{d.desc}</span>}
            </span>
          </Link>
        ))}
      </div>
    </div>
  );
}

export function Navbar() {
  return (
    <header className="sticky top-0 z-[60] flex w-full justify-center px-6 py-[18px]">
      <div className="flex w-full max-w-[1240px] items-center gap-6">
        <Link href="/" className="flex items-center gap-[11px] no-underline">
          <LogoMark />
          <Wordmark name={site.brand.name} />
        </Link>

        <nav className="mx-auto hidden items-center gap-1.5 text-sm font-medium text-[#4a4a46] lg:flex">
          {site.nav.map((item) =>
            item.dropdown ? (
              <div key={item.label} className="group relative">
                <button className="flex items-center gap-1.5 rounded-full px-[13px] py-[9px] font-semibold text-ink transition-colors hover:bg-ink/5">
                  {item.label}
                  <ChevronDown
                    size={13}
                    strokeWidth={2.4}
                    className="transition-transform duration-300 group-hover:rotate-180"
                  />
                </button>
                <Dropdown item={item} />
              </div>
            ) : (
              <Link
                key={item.label}
                href={item.href}
                className="flex items-center gap-1.5 rounded-full px-[13px] py-[9px] text-[#4a4a46] no-underline transition-colors hover:bg-ink/5"
              >
                {item.label}
                {item.dot && (
                  <span className="h-[7px] w-[7px] rounded-full bg-orange shadow-[0_0_0_3px_rgba(230,82,47,0.18)]" />
                )}
              </Link>
            )
          )}
        </nav>

        <div className="ml-auto flex items-center gap-2.5 lg:ml-0">
          <button
            aria-label="Account"
            className="flex h-[42px] w-[42px] items-center justify-center rounded-full bg-white shadow-[0_6px_16px_-8px_rgba(20,20,18,0.3)] transition hover:-translate-y-px"
          >
            <User size={18} color="#171715" strokeWidth={2} />
          </button>
          <button
            aria-label="Theme"
            className="flex h-[42px] w-[42px] items-center justify-center rounded-full bg-white shadow-[0_6px_16px_-8px_rgba(20,20,18,0.3)] transition hover:-translate-y-px"
          >
            <Sun size={18} color="#171715" strokeWidth={2} />
          </button>
        </div>
      </div>
    </header>
  );
}
