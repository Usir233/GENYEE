<template>
  <div
    class="suggested-questions-container"
    :class="{ 'has-questions': questions.length > 0 || loading }"
  >
    <div v-if="loading && questions.length === 0" class="suggested-questions-inner">
      <div class="suggested-questions-title">
        <t-skeleton animation="gradient" :row-col="[{ width: '120px', height: '14px' }]" />
      </div>
      <div class="suggested-questions-grid">
        <div v-for="n in 6" :key="'sq-skel-' + n" class="suggested-question-card sq-card-skeleton">
          <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '14px', type: 'rect' }]" />
        </div>
      </div>
    </div>
    <transition v-else appear name="sq-fade">
      <div v-if="questions.length > 0" class="suggested-questions-inner">
        <div class="suggested-questions-title-row">
          <p class="suggested-questions-caption">
            <span class="suggested-questions-title">{{ t('chat.suggestedQuestions') }}</span>
            <button
              type="button"
              class="suggested-questions-refresh"
              :disabled="loading"
              :title="t('chat.refreshSuggestedQuestions')"
              :aria-label="t('chat.refreshSuggestedQuestions')"
              @click="$emit('refresh')"
            >
              <t-icon
                :name="loading ? 'loading' : 'refresh'"
                :class="{ 'sq-refresh-spin': loading }"
              />
            </button>
          </p>
        </div>
        <div class="suggested-questions-grid">
          <button
            v-for="item in questions"
            :key="item.question"
            type="button"
            class="suggested-question-card"
            @click="$emit('select', item)"
          >
            <span class="suggested-question-text">{{ item.question }}</span>
            <span v-if="item.source === 'faq'" class="suggested-question-badge faq">FAQ</span>
          </button>
        </div>
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import type { SuggestedQuestion } from '@/api/agent';

defineProps<{
  questions: SuggestedQuestion[];
  loading: boolean;
}>();

defineEmits<{
  (event: 'refresh'): void;
  (event: 'select', item: SuggestedQuestion): void;
}>();

const { t } = useI18n();
</script>

<style lang="less" scoped>
@import '../../../components/css/suggested-questions.less';

@keyframes contentFadeIn {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

button.suggested-question-card {
  font: inherit;
  text-align: start;
}

.suggested-questions-container {
  transition: min-height var(--gy-motion-slow) @suggested-ease;
}

.suggested-questions-inner {
  animation: contentFadeIn var(--gy-motion-slow) ease-out;
}

.sq-fade-enter-active,
.sq-fade-leave-active {
  transition: opacity var(--gy-motion-base) @suggested-ease;
}

.sq-fade-enter-from,
.sq-fade-leave-to {
  opacity: 0;
}
</style>
