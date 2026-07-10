import type { Metadata } from "next";
import "./globals.css";

const description =
  "Ariadne is a local-first multilingual memory server for Codex, Claude Code, and MCP clients, powered by Qdrant, Ollama, and bge-m3 hybrid search.";
const siteUrl = "https://ariadne-memory.mclaut124670.chatgpt.site";
const socialImage = `${siteUrl}/og.png`;

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
  alternates: { canonical: "/" },
  openGraph: {
    type: "website",
    title: "Ariadne - Local-first memory for AI agents",
    description,
    siteName: "Ariadne",
    url: "/",
    images: [{ url: socialImage, width: 1280, height: 640, alt: "Ariadne local-first AI memory" }],
  },
  twitter: {
    card: "summary_large_image",
    title: "Ariadne - Local-first memory for AI agents",
    description,
    images: [socialImage],
  },
  icons: {
    icon: "/favicon.svg",
    shortcut: "/favicon.svg",
  },
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
