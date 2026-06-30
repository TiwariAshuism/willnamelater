import type { Metadata } from "next";
import { Inter } from "next/font/google";
import "./globals.css";

const inter = Inter({
  variable: "--font-inter",
  subsets: ["latin"],
  weight: ["400", "500", "600", "700", "800", "900"],
});

export const metadata: Metadata = {
  title: "Pallet Ross — A place to display your masterpiece",
  description:
    "Pallet Ross is the art marketplace where artists display their masterpieces and buyers discover and acquire one-of-a-kind work from a global community.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className={`${inter.variable} antialiased`}>
      <body className="overflow-x-clip bg-canvas text-ink">{children}</body>
    </html>
  );
}
