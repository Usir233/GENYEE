import { computed, nextTick, type ComputedRef, type Ref } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { transcribeVoice } from '@/api/chat';
import type { AgentConfig } from '@/api/agent';
import { isVoiceInputSupported, useVoiceInput, type VoiceInputError } from '@/composables/useVoiceInput';
import { createHoldToTalk, voiceHotkeyLabel } from '@/utils/voiceHotkey';

const voiceErrorMessages: Record<VoiceInputError, string> = {
  unsupported: 'input.voiceUnsupported',
  insecure: 'input.voiceInsecureContext',
  permission: 'input.voicePermissionDenied',
  device: 'input.voiceDeviceError',
  decode: 'input.voiceDecodeError',
  transcribe: 'input.voiceTranscribeFailed',
  empty: 'input.voiceEmptyResult',
  releasedEarly: 'input.voiceReleasedEarly',
};

export function useChatInputVoice(options: {
  query: Ref<string>;
  isComposing: Ref<boolean>;
  isEmbeddedMode: () => boolean;
  hasAgentConfig: ComputedRef<boolean>;
  currentAgentConfig: ComputedRef<Partial<AgentConfig>>;
  selectedAgentId: ComputedRef<string>;
  getSelectedAgentSourceTenantId: () => string | undefined;
  getLocale: () => string;
  getTextareaEl: () => HTMLTextAreaElement | null;
  t: (key: string, params?: Record<string, unknown>) => string;
}) {
  const {
    query,
    isComposing,
    isEmbeddedMode,
    hasAgentConfig,
    currentAgentConfig,
    selectedAgentId,
    getSelectedAgentSourceTenantId,
    getLocale,
    getTextareaEl,
    t,
  } = options;

  const voiceInputSupported = isVoiceInputSupported();
  const showVoiceButton = computed(() => {
    if (isEmbeddedMode() || !voiceInputSupported || !hasAgentConfig.value) return false;
    const config = currentAgentConfig.value;
    return config?.audio_upload_enabled === true && Boolean(config?.asr_model_id);
  });
  const voiceHotkeyText = voiceHotkeyLabel();

  let voiceInsertRange: { start: number; end: number } | null = null;

  const insertVoiceText = (text: string): void => {
    const current = query.value;
    const range = voiceInsertRange ?? { start: current.length, end: current.length };
    const start = Math.min(range.start, current.length);
    const end = Math.min(Math.max(range.end, start), current.length);
    const before = current.slice(0, start);
    const after = current.slice(end);
    const lead =
      before && !/\s$/.test(before) && !/^[\u3000-\u303f\uff00-\uffef]/.test(text) ? ' ' : '';
    const inserted = lead + text;
    query.value = before + inserted + after;
    voiceInsertRange = null;
    void nextTick(() => {
      const textarea = getTextareaEl();
      if (!textarea) return;
      textarea.focus({ preventScroll: true });
      const caret = before.length + inserted.length;
      textarea.setSelectionRange(caret, caret);
    });
  };

  const voiceInput = useVoiceInput({
    target: () =>
      showVoiceButton.value
        ? {
            agentId: selectedAgentId.value,
            agentSourceTenantId: getSelectedAgentSourceTenantId(),
            locale: getLocale(),
          }
        : null,
    transcribe: async (wav, target) => {
      const res = await transcribeVoice(
        wav,
        target.agentId,
        target.agentSourceTenantId,
        target.locale,
      );
      return res?.data?.text ?? '';
    },
    onText: insertVoiceText,
    onError: (error, detail) => {
      if (detail) console.warn('[voice input]', error, detail);
      const message = t(voiceErrorMessages[error]);
      if (error === 'empty' || error === 'releasedEarly') MessagePlugin.info(message);
      else MessagePlugin.warning(message);
    },
  });

  const voiceState = voiceInput.state;
  const voiceElapsedText = computed(() => {
    const seconds = Math.floor(voiceInput.elapsedMs.value / 1000);
    return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
  });

  const startVoiceInput = (): void => {
    if (!showVoiceButton.value || voiceState.value !== 'idle') return;
    const textarea = getTextareaEl();
    voiceInsertRange =
      textarea && document.activeElement === textarea
        ? { start: textarea.selectionStart, end: textarea.selectionEnd }
        : null;
    void voiceInput.start();
  };

  const stopVoiceInput = (): void => voiceInput.stop();

  const voiceHotkeyAllowed = (): boolean => {
    if (!showVoiceButton.value || voiceState.value !== 'idle') return false;
    const textarea = getTextareaEl();
    if (!textarea?.isConnected || textarea.offsetParent === null) return false;
    const active = document.activeElement as HTMLElement | null;
    if (active && active !== textarea && active !== document.body) {
      if (active.closest('[role="dialog"], .t-dialog, .t-drawer')) return false;
      if (active.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(active.tagName)) {
        return false;
      }
    }
    return !isComposing.value;
  };

  const voiceHold = createHoldToTalk({
    enabled: voiceHotkeyAllowed,
    onStart: startVoiceInput,
    onStop: stopVoiceInput,
  });

  const onVoiceKeydown = (e: KeyboardEvent): void => voiceHold.keydown(e);
  const onVoiceKeyup = (e: KeyboardEvent): void => voiceHold.keyup(e);
  const onVoiceWindowBlur = (): void => voiceHold.release();
  const onVoiceVisibilityChange = (): void => {
    if (document.visibilityState === 'hidden') voiceHold.release();
  };

  const onVoiceButtonPointerDown = (e: PointerEvent): void => {
    if (e.button !== 0) return;
    e.preventDefault();
    (e.currentTarget as HTMLElement | null)?.setPointerCapture?.(e.pointerId);
    startVoiceInput();
  };
  const onVoiceButtonPointerUp = (): void => stopVoiceInput();

  return {
    showVoiceButton,
    voiceHotkeyText,
    voiceState,
    voiceElapsedText,
    onVoiceKeydown,
    onVoiceKeyup,
    onVoiceWindowBlur,
    onVoiceVisibilityChange,
    onVoiceButtonPointerDown,
    onVoiceButtonPointerUp,
  };
}
