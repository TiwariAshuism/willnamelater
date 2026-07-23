import { redirect } from "next/navigation";
import { AuthForm } from "@/components/auth/AuthForm";
import { Card } from "@/components/ui/Card";
import { getAccessToken } from "@/lib/session";

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ next?: string }>;
}) {
  if (await getAccessToken()) {
    redirect("/dashboard");
  }
  const { next } = await searchParams;

  return (
    <main className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <h1 className="mb-1 text-lg font-semibold">Sign in to InfluAudit</h1>
        <p className="mb-6 text-sm text-[var(--ink-secondary)]">
          Audit your influence and authenticity.
        </p>
        <AuthForm mode="login" next={next} />
      </Card>
    </main>
  );
}
