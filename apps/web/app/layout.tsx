import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "InfluAudit — Influencer authenticity, audited",
  description:
    "Connect your accounts, run an audit, and see your influence and authenticity score with fraud signals and an AI-written report.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
