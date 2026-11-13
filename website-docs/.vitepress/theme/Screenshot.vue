<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useData, withBase } from 'vitepress'

const props = defineProps<{
  /** 相对站点根的图片路径，约定放在 public/screenshots/ 下 */
  src: string
  /** 图注：说明这张图展示的是什么 */
  caption: string
  /** 补充说明：截图应覆盖哪些界面元素，供补图的人参考 */
  hint?: string
}>()

const { theme } = useData()
const size = computed(() => theme.value.genyeeScreenshotSizes?.[props.src])
const failed = ref(false)
const resolved = computed(() => withBase(props.src))
watch(resolved, () => { failed.value = false })
</script>

<template>
  <figure class="gy-shot">
    <div class="gy-shot-media" :style="size ? { aspectRatio: `${size.width} / ${size.height}` } : undefined">
      <img
        v-if="size && !failed"
        class="gy-shot-img"
        :src="resolved"
        :alt="caption"
        :width="size.width"
        :height="size.height"
        @error="failed = true"
      />
      <div v-else class="gy-shot-placeholder" :class="{ 'gy-shot-placeholder-sized': size }">
        <div class="gy-shot-placeholder-badge">{{ failed ? '图片加载失败' : '截图待补充' }}</div>
        <div class="gy-shot-placeholder-caption">{{ caption }}</div>
        <p v-if="hint" class="gy-shot-placeholder-hint">{{ hint }}</p>
        <code class="gy-shot-placeholder-path">website-docs/public{{ src }}</code>
      </div>
    </div>
    <figcaption class="gy-shot-caption">{{ caption }}</figcaption>
  </figure>
</template>

<style scoped>
.gy-shot {
  margin: 24px 0;
}

.gy-shot-media { position: relative; }

.gy-shot-placeholder-sized {
  position: absolute;
  inset: 0;
  justify-content: center;
  overflow: auto;
}

.gy-shot-img {
  display: block;
  width: 100%;
  height: auto;
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
}

.gy-shot-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 32px 24px;
  text-align: center;
  border: 1px dashed var(--vp-c-divider);
  border-radius: 10px;
  background: var(--vp-c-bg-soft);
}

.gy-shot-placeholder-badge {
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
  line-height: 20px;
  color: var(--vp-c-text-2);
  background: var(--vp-c-default-soft);
}

.gy-shot-placeholder-caption {
  font-size: 15px;
  font-weight: 600;
  color: var(--vp-c-text-1);
}

.gy-shot-placeholder-hint {
  max-width: 46em;
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--vp-c-text-2);
}

.gy-shot-placeholder-path {
  font-size: 12px;
  color: var(--vp-c-text-3);
}

.gy-shot-caption {
  margin-top: 8px;
  font-size: 13px;
  line-height: 1.6;
  text-align: center;
  color: var(--vp-c-text-2);
}
</style>
