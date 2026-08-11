import type { Metadata } from "next";
import "./globals.css";

const description =
  "Ariadne is local-first multilingual memory for Codex, Claude Code, and MCP clients, with project isolation and human-approved cross-project access.";
const siteUrl = "https://mclaut.github.io/ariadne";
const socialImage = `${siteUrl}/og-v0.8.8.png`;
const favicon = `${siteUrl}/favicon.svg`;

export const metadata: Metadata = {
  metadataBase: new URL(siteUrl),
  title: {
    default: "Ariadne - Local-first memory for AI agents",
    template: "%s | Ariadne",
  },
  description,
  keywords: [
    "MCP memory server",
    "Codex memory",
    "Claude Code memory",
    "local-first AI",
    "Qdrant",
    "Ollama",
    "bge-m3",
    "hybrid search",
    "multilingual embeddings",
  ],
  authors: [{ name: "Ariadne contributors" }],
  creator: "Ariadne contributors",
  alternates: {
    canonical: siteUrl,
    languages: {
      en: `${siteUrl}/`,
      uk: `${siteUrl}/uk/`,
      "x-default": `${siteUrl}/`,
    },
  },
  openGraph: {
    type: "website",
    title: "Ariadne - Local-first memory for AI agents",
    description,
    siteName: "Ariadne",
    url: siteUrl,
    images: [{ url: socialImage, width: 1200, height: 630, alt: "Ariadne local-first AI memory" }],
  },
  twitter: {
    card: "summary_large_image",
    title: "Ariadne - Local-first memory for AI agents",
    description,
    images: [socialImage],
  },
  icons: {
    icon: favicon,
    shortcut: favicon,
  },
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
