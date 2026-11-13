import 'vitepress'

declare module 'vitepress/dist/client/theme-default/config' {
  export interface ThemeConfig {
    /** 文档站点展示的 GENYEE 发布版本（来自仓库根 VERSION） */
    genyeeVersion?: string
    /** Intrinsic screenshot dimensions used to reserve layout before image loading. */
    genyeeScreenshotSizes?: Record<string, { width: number; height: number }>
  }
}
