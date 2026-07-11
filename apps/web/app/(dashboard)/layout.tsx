import Link from "next/link";
import { getCurrentUser } from "@/lib/auth";
import { LogoutButton } from "@/components/LogoutButton";

const navItems = [
  { href: "/dashboard", label: "Overview" },
  { href: "/audits", label: "Audits" },
  { href: "/connections", label: "Connections" },
];

// The admin area is shown only to admins; the backend also enforces the role on
// every /admin request, so the nav gate is convenience, not the security
// boundary.
const adminItem = { href: "/admin", label: "Admin" };

export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // Redirects to /login if the session is missing or rejected.
  const user = await getCurrentUser();

  const items =
    user.role === "admin" ? [...navItems, adminItem] : navItems;

  return (
    <div className="min-h-screen">
      <header className="border-b border-[var(--line)] bg-[var(--surface)]">
        <div className="mx-auto flex max-w-5xl items-center justify-between gap-4 px-6 py-3">
          <div className="flex items-center gap-6">
            <Link href="/dashboard" className="font-semibold">
              InfluAudit
            </Link>
            <nav className="flex items-center gap-1 text-sm">
              {items.map((item) => (
                <Link
                  key={item.href}
                  href={item.href}
                  className="rounded-md px-3 py-1.5 text-[var(--ink-secondary)] hover:bg-[var(--surface-2)]"
                >
                  {item.label}
                </Link>
              ))}
            </nav>
          </div>
          <div className="flex items-center gap-3">
            <span className="hidden text-sm text-[var(--ink-secondary)] sm:inline">
              {user.full_name ?? user.email}
            </span>
            <LogoutButton />
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-6 py-8">{children}</main>
    </div>
  );
}
