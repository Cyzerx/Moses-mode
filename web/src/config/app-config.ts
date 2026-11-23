import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "Artex-Moses-mode",
  version: packageJson.version,
  copyright: `© ${currentYear}, Artex-Moses-mode contributors.`,
  meta: {
    title: "Artex-Moses-mode — Autonomous Penetration Testing Console",
    description: "Console for the LLM-driven autonomous penetration testing system",
  },
};
