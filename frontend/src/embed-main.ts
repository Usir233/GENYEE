import { createApp, h } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory, RouterView } from 'vue-router'
import TDesign from 'tdesign-vue-next'
import 'tdesign-vue-next/es/style/index.css'
import '@/assets/theme/theme.css'
import '@/components/css/chat-hljs-theme.less'
import { installTDesignIconOfflineGuard } from '@/utils/tdesign-icon-offline'
import i18n from './i18n/embed'
import EmbedPage from '@/views/embed/EmbedPage.vue'
import ProtectedResourcePreview from '@/components/ProtectedResourcePreview.vue'

installTDesignIconOfflineGuard()

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/embed/:channelId',
      name: 'embed',
      component: EmbedPage,
    },
  ],
})

// Runtime-only Vue build cannot compile string templates — use a render fn.
const app = createApp({ render: () => [h(RouterView), h(ProtectedResourcePreview)] })

// 全局错误处理：捕获未处理的嵌入组件错误与未处理 Promise 拒绝，防止白屏
app.config.errorHandler = (err, instance, info) => {
  console.error('[GENYEE Embed] Unhandled Vue error:', err, '\nComponent:', instance, '\nInfo:', info)
}

if (typeof window !== 'undefined') {
  window.addEventListener('unhandledrejection', (event) => {
    console.error('[GENYEE Embed] Unhandled Promise rejection:', event.reason)
  })
}

app.use(TDesign)
app.use(createPinia())
app.use(router)
app.use(i18n)

router.isReady().finally(() => {
  app.mount('#embed-app')
})
