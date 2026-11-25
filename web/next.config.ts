import type { NextConfig } from "next";
const config: NextConfig = {
  output: "export",
  poweredByHeader: false,
  devIndicators: false,
  experimental: { cpus: 2 },
  generateBuildId: async () => process.env.INBOX_BUILD_ID || null,
};
export default config;
