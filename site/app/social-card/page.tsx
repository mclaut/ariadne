import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Ariadne social card",
  robots: { index: false, follow: false },
};

export default function SocialCard() {
  const basePath = process.env.GITHUB_PAGES === "true" ? "/ariadne" : "";

  return (
    <main className="social-card-page" aria-label="Ariadne social preview">
      <div className="social-card-copy">
        <div className="social-card-kicker">
          <span className="status-dot" />
          Local infrastructure
        </div>
        <h1>ARIADNE</h1>
        <p>Local-first memory for AI agents</p>
      </div>
      <div
        className="social-card-visual"
        style={{ backgroundImage: `url("${basePath}/og-visual.png")` }}
        aria-hidden="true"
      />
      <div className="social-card-footer">
        <span>v0.3.0</span>
        <span>Windows / macOS / Linux</span>
        <span>MIT + MCP</span>
      </div>
    </main>
  );
}
