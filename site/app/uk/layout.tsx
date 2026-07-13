import type { Metadata } from "next";

const siteUrl = "https://mclaut.github.io/ariadne";
const description =
  "Ariadne — локальна багатомовна довготривала пам’ять для Codex, Claude Code та MCP-клієнтів.";

export const metadata: Metadata = {
  title: "Ariadne — локальна пам’ять для AI-агентів",
  description,
  alternates: {
    canonical: `${siteUrl}/uk/`,
    languages: {
      en: `${siteUrl}/`,
      uk: `${siteUrl}/uk/`,
      "x-default": `${siteUrl}/`,
    },
  },
  openGraph: {
    type: "website",
    title: "Ariadne — локальна пам’ять для AI-агентів",
    description,
    locale: "uk_UA",
    url: `${siteUrl}/uk/`,
  },
};

export default function UkrainianLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return children;
}
