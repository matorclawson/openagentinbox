import "./style.css";
import type { Metadata } from "next";
export const metadata: Metadata = {
  title: "OpenAgentInbox",
  icons: {icon:"/brand/icon.png"},
  description: "A shared inbox for your agents.",
};
export default function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
