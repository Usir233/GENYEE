import type { Metadata } from "next";
import "./globals.css";
import { themeInitializationScript } from "./theme";

export const metadata: Metadata = {
  title: "GENYEE — 帮你找到答案，并将知识付诸实践",
  description: "企业级知识管理框架 GENYEE，集 RAG 问答、Agent 推理与自动 Wiki 于一体。v0.9.0 新增 GENYEE 助手、多智能体协作、自动化与执行权限审批；支持私有化部署。",
  // Reuse the documentation favicon so the homepage adds nothing at the site root.
  icons: { icon: "/docs/favicon.ico" },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="zh-CN" className="h-full antialiased" suppressHydrationWarning>
      <head><script dangerouslySetInnerHTML={{ __html: themeInitializationScript }} /></head>
      <body className="min-h-full flex flex-col">{children}</body>
    </html>
  );
}
