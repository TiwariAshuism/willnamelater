import { redirect } from "next/navigation";
import { AuthForm } from "@/components/auth/AuthForm";
import { Card } from "@/components/ui/Card";
import { getAccessToken } from "@/lib/session";

export default async function RegisterPage() {
  if (await getAccessToken()) {
    redirect("/dashboard");
  }

  return (
    <main className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <h1 className="mb-1 text-lg font-semibold">Create your account</h1>
        <p className="mb-6 text-sm text-[var(--ink-secondary)]">
          Start auditing in minutes.
        </p>
        <AuthForm mode="register" />
      </Card>
    </main>
  );
}
