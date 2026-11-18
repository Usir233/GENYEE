<template>
  <div v-show="currentSection === 'prompts'" class="section section--prompts">
    <div class="prompts-panel">
      <div class="prompts-panel__header">
        <div class="section-header section-header--compact">
          <h2>{{ $t('agent.editor.promptsConfig') }}</h2>
          <p class="section-description">{{ $t('agent.editor.promptsConfigDesc') }}</p>
        </div>

        <nav
          v-if="promptNavItems.length > 1"
          class="prompts-outline"
          :aria-label="$t('agentEditor.promptNav.ariaLabel')"
        >
          <button
            v-for="item in promptNavItems"
            :key="item.key"
            type="button"
            class="prompts-outline__pill"
            :class="{ 'prompts-outline__pill--active': activePromptAnchor === item.key }"
            @click="emit('update:activePromptAnchor', item.key)"
          >
            <span>{{ item.label }}</span>
            <span
              v-if="item.customized"
              class="prompts-outline__dot"
              :title="$t('agentEditor.intentPrompts.customized')"
            />
          </button>
        </nav>
      </div>

      <div class="prompts-panel__body">
        <div class="settings-group">
          <!-- System Prompt -->
          <div
            v-show="activePromptAnchor === 'system'"
            class="setting-row setting-row-vertical prompts-panel__pane"
          >
            <div class="setting-info">
              <label>
                {{ $t('agent.editor.systemPrompt') }}
                <span v-if="!isBuiltinAgent" class="required">*</span>
              </label>
              <p class="desc">
                {{ $t('agentEditor.desc.systemPrompt') }}{{
                  isBuiltinAgent ? $t('agentEditor.desc.leaveEmptyDefault') : ''
                }}
              </p>
              <p class="desc">{{ $t('agentEditor.desc.promptInheritance') }}</p>
              <div class="placeholder-tags">
                <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                <t-tooltip
                  v-for="placeholder in availablePlaceholders"
                  :key="placeholder.name"
                  :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                  placement="top"
                >
                  <button
                    type="button"
                    class="placeholder-tag"
                    @click="emit('placeholderClick', 'system', placeholder.name)"
                    v-text="'{{' + placeholder.name + '}}'"
                  />
                </t-tooltip>
                <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
              </div>
            </div>
            <div class="setting-control setting-control-full" style="position: relative;">
              <div v-if="isAgentMode" class="textarea-with-template">
                <t-textarea
                  :ref="(el) => setPromptTextareaRef('system', el)"
                  :model-value="formData.config.system_prompt"
                  :placeholder="$t('agent.editor.systemPromptPlaceholder')"
                  :autosize="{ minRows: 10, maxRows: 25 }"
                  class="system-prompt-textarea"
                  @update:model-value="(val: unknown) => emit('updateConfig', { system_prompt: String(val ?? '') })"
                  @input="emit('promptFieldInput', 'system')"
                />
                <div class="textarea-actions">
                  <AssistantActionButton
                    class="prompt-assistant-action"
                    :label="$t(formData.config.system_prompt?.trim() ? 'assistant.actions.refinePrompt' : 'assistant.actions.writePrompt')"
                    :query="$t(formData.config.system_prompt?.trim() ? 'assistant.prompts.refinePrompt' : 'assistant.prompts.writePrompt')"
                    :selection="{ field: $t('agent.editor.systemPrompt'), text: formData.config.system_prompt || '' }"
                  />
                  <PromptTemplateSelector
                    type="agentSystemPrompt"
                    position="inline"
                    :hasKnowledgeBase="hasKnowledgeBase"
                    @select="(tmpl: PromptTemplate) => emit('systemPromptTemplateSelect', tmpl)"
                    @reset-default="(tmpl: PromptTemplate) => emit('agentSystemPromptResetDefault', tmpl)"
                  />
                </div>
              </div>
              <div v-else class="textarea-with-template">
                <t-textarea
                  :ref="(el) => setPromptTextareaRef('system', el)"
                  :model-value="formData.config.system_prompt"
                  :placeholder="$t('agent.editor.systemPromptPlaceholder')"
                  :autosize="{ minRows: 10, maxRows: 25 }"
                  class="system-prompt-textarea"
                  @update:model-value="(val: unknown) => emit('updateConfig', { system_prompt: String(val ?? '') })"
                  @input="emit('promptFieldInput', 'system')"
                />
                <div class="textarea-actions">
                  <AssistantActionButton
                    class="prompt-assistant-action"
                    :label="$t(formData.config.system_prompt?.trim() ? 'assistant.actions.refinePrompt' : 'assistant.actions.writePrompt')"
                    :query="$t(formData.config.system_prompt?.trim() ? 'assistant.prompts.refinePrompt' : 'assistant.prompts.writePrompt')"
                    :selection="{ field: $t('agent.editor.systemPrompt'), text: formData.config.system_prompt || '' }"
                  />
                  <PromptTemplateSelector
                    type="systemPrompt"
                    position="inline"
                    :hasKnowledgeBase="hasKnowledgeBase"
                    @select="(tmpl: PromptTemplate) => emit('systemPromptTemplateSelect', tmpl)"
                    @reset-default="(tmpl: PromptTemplate) => emit('systemPromptTemplateSelect', tmpl)"
                  />
                </div>
              </div>
              <Teleport to="body">
                <div
                  v-if="promptPopups.system.show && filteredPlaceholdersByType.system.length > 0"
                  class="placeholder-popup-wrapper"
                  :style="promptPopups.system.style"
                >
                  <div class="placeholder-popup">
                    <div
                      v-for="(placeholder, index) in filteredPlaceholdersByType.system"
                      :key="placeholder.name"
                      class="placeholder-item"
                      :class="{ active: promptPopups.system.selectedIndex === index }"
                      @mousedown.prevent="emit('insertPromptPlaceholder', 'system', placeholder.name, true)"
                      @mouseenter="emit('hoverPopupItem', 'system', index)"
                    >
                      <div class="placeholder-name">
                        <code v-text="'{{' + placeholder.name + '}}'"></code>
                      </div>
                      <div class="placeholder-desc">{{ placeholder.description }}</div>
                    </div>
                  </div>
                </div>
              </Teleport>
            </div>
          </div>

          <!-- Context Template -->
          <div
            v-if="!isAgentMode"
            v-show="activePromptAnchor === 'context'"
            class="setting-row setting-row-vertical prompts-panel__pane"
          >
            <div class="setting-info">
              <label>
                {{ $t('agent.editor.contextTemplate') }}
                <span v-if="!isBuiltinAgent" class="required">*</span>
              </label>
              <p class="desc">
                {{ $t('agentEditor.desc.contextTemplate') }}{{
                  isBuiltinAgent ? $t('agentEditor.desc.leaveEmptyDefault') : ''
                }}
              </p>
              <div class="placeholder-tags">
                <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                <t-tooltip
                  v-for="placeholder in contextTemplatePlaceholders"
                  :key="placeholder.name"
                  :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                  placement="top"
                >
                  <button
                    type="button"
                    class="placeholder-tag"
                    @click="emit('placeholderClick', 'context', placeholder.name)"
                    v-text="'{{' + placeholder.name + '}}'"
                  />
                </t-tooltip>
                <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
              </div>
            </div>
            <div class="setting-control setting-control-full" style="position: relative;">
              <div class="textarea-with-template">
                <t-textarea
                  :ref="(el) => setPromptTextareaRef('context', el)"
                  :model-value="formData.config.context_template"
                  :placeholder="$t('agent.editor.contextTemplatePlaceholder')"
                  :autosize="{ minRows: 8, maxRows: 20 }"
                  class="system-prompt-textarea"
                  @update:model-value="(val: unknown) => emit('updateConfig', { context_template: String(val ?? '') })"
                  @input="emit('promptFieldInput', 'context')"
                />
                <div class="textarea-actions textarea-actions--right">
                  <PromptTemplateSelector
                    type="contextTemplate"
                    position="inline"
                    :hasKnowledgeBase="hasKnowledgeBase"
                    @select="(tmpl: PromptTemplate) => emit('contextTemplateSelect', tmpl)"
                    @reset-default="(tmpl: PromptTemplate) => emit('contextTemplateSelect', tmpl)"
                  />
                </div>
              </div>
              <Teleport to="body">
                <div
                  v-if="promptPopups.context.show && filteredPlaceholdersByType.context.length > 0"
                  class="placeholder-popup-wrapper"
                  :style="promptPopups.context.style"
                >
                  <div class="placeholder-popup">
                    <div
                      v-for="(placeholder, index) in filteredPlaceholdersByType.context"
                      :key="placeholder.name"
                      class="placeholder-item"
                      :class="{ active: promptPopups.context.selectedIndex === index }"
                      @mousedown.prevent="emit('insertPromptPlaceholder', 'context', placeholder.name, true)"
                      @mouseenter="emit('hoverPopupItem', 'context', index)"
                    >
                      <div class="placeholder-name">
                        <code v-text="'{{' + placeholder.name + '}}'"></code>
                      </div>
                      <div class="placeholder-desc">{{ placeholder.description }}</div>
                    </div>
                  </div>
                </div>
              </Teleport>
            </div>
          </div>

          <!-- Intent Prompts -->
          <div
            v-if="!isAgentMode"
            v-show="activePromptAnchor === 'intent'"
            class="setting-row setting-row-vertical prompts-panel__pane"
          >
            <div class="setting-info">
              <label>{{ $t('agentEditor.intentPrompts.title') }}</label>
              <p class="desc">{{ $t('agentEditor.intentPrompts.sectionDesc') }}</p>
            </div>
            <div class="setting-control setting-control-full">
              <div class="intent-prompts-editor">
                <div v-if="intentPromptTemplates.length === 0" class="prompt-disabled-hint">
                  {{ $t('agentEditor.intentPrompts.empty') }}
                </div>
                <template v-else>
                  <div
                    class="intent-toggle-group"
                    role="tablist"
                    :aria-label="$t('agentEditor.intentPrompts.intentLabel')"
                  >
                    <t-button
                      v-for="template in intentPromptTemplates"
                      :key="template.id"
                      theme="default"
                      variant="outline"
                      size="small"
                      class="intent-toggle-btn"
                      :class="{ 'intent-toggle-btn--active': selectedIntent === template.id }"
                      :disabled="readOnly"
                      @click="emit('update:selectedIntent', template.id)"
                    >
                      <span class="intent-toggle-label">
                        {{ template.name || template.id }}
                        <t-tooltip
                          v-if="isIntentCustomized(template.id)"
                          :content="$t('agentEditor.intentPrompts.customized')"
                          placement="top"
                        >
                          <span class="intent-toggle-dot" />
                        </t-tooltip>
                      </span>
                    </t-button>
                  </div>
                  <p v-if="currentIntentTemplateDesc" class="intent-active-desc">
                    {{ currentIntentTemplateDesc }}
                  </p>

                  <div v-if="placeholderData.system_prompt.length > 0" class="placeholder-tags">
                    <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                    <t-tooltip
                      v-for="placeholder in placeholderData.system_prompt"
                      :key="placeholder.name"
                      :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                      placement="top"
                    >
                      <button
                        type="button"
                        class="placeholder-tag"
                        @click="emit('placeholderClick', 'intent', placeholder.name)"
                        v-text="'{{' + placeholder.name + '}}'"
                      />
                    </t-tooltip>
                    <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
                  </div>

                  <div class="textarea-with-template">
                    <t-textarea
                      :ref="(el) => setPromptTextareaRef('intent', el)"
                      :model-value="intentEditorValue"
                      class="system-prompt-textarea"
                      :autosize="{ minRows: 10, maxRows: 25 }"
                      :disabled="readOnly || !selectedIntent"
                      :placeholder="currentIntentPlaceholder || $t('agentEditor.intentPrompts.promptPlaceholder')"
                      @update:model-value="(val: unknown) => emit('update:intentEditorValue', String(val ?? ''))"
                      @input="emit('promptFieldInput', 'intent')"
                    />
                    <div class="textarea-actions textarea-actions--right">
                      <PromptTemplateSelector
                        type="intentPrompt"
                        position="inline"
                        :intent-id="selectedIntent"
                        :show-template-picker="false"
                        @reset-default="emit('resetCurrentIntentPrompt')"
                      />
                    </div>
                  </div>

                  <Teleport to="body">
                    <div
                      v-if="promptPopups.intent.show && filteredPlaceholdersByType.intent.length > 0"
                      class="placeholder-popup-wrapper"
                      :style="promptPopups.intent.style"
                    >
                      <div class="placeholder-popup">
                        <div
                          v-for="(placeholder, index) in filteredPlaceholdersByType.intent"
                          :key="placeholder.name"
                          class="placeholder-item"
                          :class="{ active: promptPopups.intent.selectedIndex === index }"
                          @mousedown.prevent="emit('insertPromptPlaceholder', 'intent', placeholder.name, true)"
                          @mouseenter="emit('hoverPopupItem', 'intent', index)"
                        >
                          <div class="placeholder-name">
                            <code v-text="'{{' + placeholder.name + '}}'" />
                          </div>
                          <div class="placeholder-desc">{{ placeholder.description }}</div>
                        </div>
                      </div>
                    </div>
                  </Teleport>
                </template>
              </div>
            </div>
          </div>

          <!-- Rewrite Prompts -->
          <template
            v-if="!isAgentMode && formData.config.multi_turn_enabled && formData.config.enable_rewrite"
          >
            <div
              v-show="activePromptAnchor === 'rewrite-system'"
              class="setting-row setting-row-vertical prompts-panel__pane"
            >
              <div class="setting-info">
                <label>{{ $t('agent.editor.rewritePromptSystem') }}</label>
                <p class="desc">{{ $t('agentEditor.desc.rewriteSystemPrompt') }}</p>
                <div v-if="rewriteSystemPlaceholders.length > 0" class="placeholder-tags">
                  <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                  <t-tooltip
                    v-for="placeholder in rewriteSystemPlaceholders"
                    :key="placeholder.name"
                    :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                    placement="top"
                  >
                    <button
                      type="button"
                      class="placeholder-tag"
                      @click="emit('placeholderClick', 'rewriteSystem', placeholder.name)"
                      v-text="'{{' + placeholder.name + '}}'"
                    />
                  </t-tooltip>
                  <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
                </div>
              </div>
              <div class="setting-control setting-control-full" style="position: relative;">
                <div class="textarea-with-template">
                  <t-textarea
                    :ref="(el) => setPromptTextareaRef('rewriteSystem', el)"
                    :model-value="formData.config.rewrite_prompt_system"
                    :placeholder="defaultRewritePromptSystem || $t('agent.editor.rewritePromptSystemPlaceholder')"
                    :autosize="{ minRows: 4, maxRows: 10 }"
                    @update:model-value="(val: unknown) => emit('updateConfig', { rewrite_prompt_system: String(val ?? '') })"
                    @input="emit('promptFieldInput', 'rewriteSystem')"
                  />
                  <div class="textarea-actions textarea-actions--right">
                    <PromptTemplateSelector
                      type="rewrite"
                      position="inline"
                      @select="(tmpl: PromptTemplate) => emit('rewriteTemplateSelect', tmpl)"
                      @reset-default="(tmpl: PromptTemplate) => emit('rewriteTemplateSelect', tmpl)"
                    />
                  </div>
                </div>
                <Teleport to="body">
                  <div
                    v-if="promptPopups.rewriteSystem.show && filteredPlaceholdersByType.rewriteSystem.length > 0"
                    class="placeholder-popup-wrapper"
                    :style="promptPopups.rewriteSystem.style"
                  >
                    <div class="placeholder-popup">
                      <div
                        v-for="(placeholder, index) in filteredPlaceholdersByType.rewriteSystem"
                        :key="placeholder.name"
                        class="placeholder-item"
                        :class="{ active: promptPopups.rewriteSystem.selectedIndex === index }"
                        @mousedown.prevent="emit('insertPromptPlaceholder', 'rewriteSystem', placeholder.name, true)"
                        @mouseenter="emit('hoverPopupItem', 'rewriteSystem', index)"
                      >
                        <div class="placeholder-name">
                          <code v-text="'{{' + placeholder.name + '}}'"></code>
                        </div>
                        <div class="placeholder-desc">{{ placeholder.description }}</div>
                      </div>
                    </div>
                  </div>
                </Teleport>
              </div>
            </div>

            <div
              v-show="activePromptAnchor === 'rewrite-user'"
              class="setting-row setting-row-vertical prompts-panel__pane"
            >
              <div class="setting-info">
                <label>{{ $t('agent.editor.rewritePromptUser') }}</label>
                <p class="desc">{{ $t('agentEditor.desc.rewriteUserPrompt') }}</p>
                <div v-if="rewritePlaceholders.length > 0" class="placeholder-tags">
                  <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                  <t-tooltip
                    v-for="placeholder in rewritePlaceholders"
                    :key="placeholder.name"
                    :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                    placement="top"
                  >
                    <button
                      type="button"
                      class="placeholder-tag"
                      @click="emit('placeholderClick', 'rewriteUser', placeholder.name)"
                      v-text="'{{' + placeholder.name + '}}'"
                    />
                  </t-tooltip>
                  <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
                </div>
              </div>
              <div class="setting-control setting-control-full" style="position: relative;">
                <div class="textarea-with-template">
                  <t-textarea
                    :ref="(el) => setPromptTextareaRef('rewriteUser', el)"
                    :model-value="formData.config.rewrite_prompt_user"
                    :placeholder="defaultRewritePromptUser || $t('agent.editor.rewritePromptUserPlaceholder')"
                    :autosize="{ minRows: 4, maxRows: 10 }"
                    @update:model-value="(val: unknown) => emit('updateConfig', { rewrite_prompt_user: String(val ?? '') })"
                    @input="emit('promptFieldInput', 'rewriteUser')"
                  />
                  <div class="textarea-actions textarea-actions--right">
                    <PromptTemplateSelector
                      type="rewrite"
                      position="inline"
                      @select="(tmpl: PromptTemplate) => emit('rewriteTemplateSelect', tmpl)"
                      @reset-default="(tmpl: PromptTemplate) => emit('rewriteTemplateSelect', tmpl)"
                    />
                  </div>
                </div>
                <Teleport to="body">
                  <div
                    v-if="promptPopups.rewriteUser.show && filteredPlaceholdersByType.rewriteUser.length > 0"
                    class="placeholder-popup-wrapper"
                    :style="promptPopups.rewriteUser.style"
                  >
                    <div class="placeholder-popup">
                      <div
                        v-for="(placeholder, index) in filteredPlaceholdersByType.rewriteUser"
                        :key="placeholder.name"
                        class="placeholder-item"
                        :class="{ active: promptPopups.rewriteUser.selectedIndex === index }"
                        @mousedown.prevent="emit('insertPromptPlaceholder', 'rewriteUser', placeholder.name, true)"
                        @mouseenter="emit('hoverPopupItem', 'rewriteUser', index)"
                      >
                        <div class="placeholder-name">
                          <code v-text="'{{' + placeholder.name + '}}'"></code>
                        </div>
                        <div class="placeholder-desc">{{ placeholder.description }}</div>
                      </div>
                    </div>
                  </div>
                </Teleport>
              </div>
            </div>
          </template>

          <!-- Fallback Strategy & Prompts -->
          <div
            v-if="!isAgentMode && hasKnowledgeBase"
            v-show="activePromptAnchor === 'fallback'"
            class="prompts-panel__pane prompts-panel__pane--stack"
          >
            <div class="setting-row">
              <div class="setting-info">
                <label>{{ $t('agent.editor.fallbackStrategy') }}</label>
                <p class="desc">{{ $t('agentEditor.desc.fallbackStrategy') }}</p>
              </div>
              <div class="setting-control">
                <t-radio-group
                  :model-value="formData.config.fallback_strategy"
                  @update:model-value="(val: unknown) => (val === 'fixed' || val === 'model') && emit('updateConfig', { fallback_strategy: val })"
                >
                  <t-radio-button value="fixed">{{ $t('agentEditor.fallback.fixed') }}</t-radio-button>
                  <t-radio-button value="model">{{ $t('agentEditor.fallback.model') }}</t-radio-button>
                </t-radio-group>
              </div>
            </div>

            <div
              v-if="formData.config.fallback_strategy === 'fixed'"
              class="setting-row setting-row-vertical"
            >
              <div class="setting-info">
                <label>{{ $t('agent.editor.fallbackResponse') }}</label>
                <p class="desc">{{ $t('agentEditor.desc.fallbackResponse') }}</p>
              </div>
              <div class="setting-control setting-control-full">
                <div class="textarea-with-template">
                  <t-textarea
                    :model-value="formData.config.fallback_response"
                    :placeholder="defaultFallbackResponse || $t('agent.editor.fallbackResponsePlaceholder')"
                    :autosize="{ minRows: 2, maxRows: 6 }"
                    @update:model-value="(val: unknown) => emit('updateConfig', { fallback_response: String(val ?? '') })"
                  />
                  <div class="textarea-actions textarea-actions--right">
                    <PromptTemplateSelector
                      type="fallback"
                      position="inline"
                      fallbackMode="fixed"
                      @select="(tmpl: PromptTemplate) => emit('fallbackResponseTemplateSelect', tmpl)"
                      @reset-default="(tmpl: PromptTemplate) => emit('fallbackResponseTemplateSelect', tmpl)"
                    />
                  </div>
                </div>
              </div>
            </div>

            <div
              v-if="formData.config.fallback_strategy === 'model'"
              class="setting-row setting-row-vertical"
            >
              <div class="setting-info">
                <label>{{ $t('agent.editor.fallbackPrompt') }}</label>
                <p class="desc">{{ $t('agentEditor.desc.fallbackPrompt') }}</p>
                <div v-if="fallbackPlaceholders.length > 0" class="placeholder-tags">
                  <span class="placeholder-label">{{ $t('agentEditor.placeholders.available') }}</span>
                  <t-tooltip
                    v-for="placeholder in fallbackPlaceholders"
                    :key="placeholder.name"
                    :content="placeholder.description + $t('agentEditor.placeholders.clickToInsert')"
                    placement="top"
                  >
                    <button
                      type="button"
                      class="placeholder-tag"
                      @click="emit('placeholderClick', 'fallback', placeholder.name)"
                      v-text="'{{' + placeholder.name + '}}'"
                    />
                  </t-tooltip>
                  <span class="placeholder-hint">{{ $t('agentEditor.placeholders.hint') }}</span>
                </div>
              </div>
              <div class="setting-control setting-control-full" style="position: relative;">
                <div class="textarea-with-template">
                  <t-textarea
                    :ref="(el) => setPromptTextareaRef('fallback', el)"
                    :model-value="formData.config.fallback_prompt"
                    :placeholder="defaultFallbackPrompt || $t('agent.editor.fallbackPromptPlaceholder')"
                    :autosize="{ minRows: 4, maxRows: 10 }"
                    @update:model-value="(val: unknown) => emit('updateConfig', { fallback_prompt: String(val ?? '') })"
                    @input="emit('promptFieldInput', 'fallback')"
                  />
                  <div class="textarea-actions textarea-actions--right">
                    <PromptTemplateSelector
                      type="fallback"
                      position="inline"
                      fallbackMode="model"
                      @select="(tmpl: PromptTemplate) => emit('fallbackPromptTemplateSelect', tmpl)"
                      @reset-default="(tmpl: PromptTemplate) => emit('fallbackPromptTemplateSelect', tmpl)"
                    />
                  </div>
                </div>
                <Teleport to="body">
                  <div
                    v-if="promptPopups.fallback.show && filteredPlaceholdersByType.fallback.length > 0"
                    class="placeholder-popup-wrapper"
                    :style="promptPopups.fallback.style"
                  >
                    <div class="placeholder-popup">
                      <div
                        v-for="(placeholder, index) in filteredPlaceholdersByType.fallback"
                        :key="placeholder.name"
                        class="placeholder-item"
                        :class="{ active: promptPopups.fallback.selectedIndex === index }"
                        @mousedown.prevent="emit('insertPromptPlaceholder', 'fallback', placeholder.name, true)"
                        @mouseenter="emit('hoverPopupItem', 'fallback', index)"
                      >
                        <div class="placeholder-name">
                          <code v-text="'{{' + placeholder.name + '}}'"></code>
                        </div>
                        <div class="placeholder-desc">{{ placeholder.description }}</div>
                      </div>
                    </div>
                  </div>
                </Teleport>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import AssistantActionButton from '@/components/assistant/AssistantActionButton.vue'
import PromptTemplateSelector from '@/components/PromptTemplateSelector.vue'
import type { PlaceholderDefinition } from '@/api/agent'
import type { PromptTemplate } from '@/api/system'
import type { AgentEditorFormConfig, AgentEditorFormData } from '@/views/agent/composables/useAgentEditorForm'
import type {
  PromptFieldType,
  PromptNavItem,
  PromptPlaceholderData,
  PromptPopupEntry,
} from '@/views/agent/composables/useAgentPromptPlaceholders'

defineProps<{
  currentSection: string
  readOnly?: boolean
  isBuiltinAgent: boolean
  isAgentMode: boolean
  hasKnowledgeBase: boolean
  formData: AgentEditorFormData
  promptNavItems: PromptNavItem[]
  activePromptAnchor: string
  placeholderData: PromptPlaceholderData
  availablePlaceholders: PlaceholderDefinition[]
  contextTemplatePlaceholders: PlaceholderDefinition[]
  rewriteSystemPlaceholders: PlaceholderDefinition[]
  rewritePlaceholders: PlaceholderDefinition[]
  fallbackPlaceholders: PlaceholderDefinition[]
  intentPromptTemplates: PromptTemplate[]
  selectedIntent: string
  intentEditorValue: string
  currentIntentPlaceholder?: string
  currentIntentTemplateDesc: string
  isIntentCustomized: (intentId: string) => boolean
  defaultRewritePromptSystem: string
  defaultRewritePromptUser: string
  defaultFallbackResponse: string
  defaultFallbackPrompt: string
  promptPopups: Record<PromptFieldType, PromptPopupEntry>
  filteredPlaceholdersByType: Record<PromptFieldType, PlaceholderDefinition[]>
  setPromptTextareaRef: (type: PromptFieldType, el: unknown) => void
}>()

const emit = defineEmits<{
  (e: 'update:activePromptAnchor', key: string): void
  (e: 'update:selectedIntent', id: string): void
  (e: 'update:intentEditorValue', value: string): void
  (e: 'updateConfig', patch: Partial<AgentEditorFormConfig>): void
  (e: 'placeholderClick', type: PromptFieldType, name: string): void
  (e: 'promptFieldInput', type: PromptFieldType): void
  (e: 'insertPromptPlaceholder', type: PromptFieldType, name: string, fromPopup: boolean): void
  (e: 'hoverPopupItem', type: PromptFieldType, index: number): void
  (e: 'resetCurrentIntentPrompt'): void
  (e: 'systemPromptTemplateSelect', tmpl: PromptTemplate): void
  (e: 'agentSystemPromptResetDefault', tmpl: PromptTemplate): void
  (e: 'contextTemplateSelect', tmpl: PromptTemplate): void
  (e: 'rewriteTemplateSelect', tmpl: PromptTemplate): void
  (e: 'fallbackResponseTemplateSelect', tmpl: PromptTemplate): void
  (e: 'fallbackPromptTemplateSelect', tmpl: PromptTemplate): void
}>()
</script>

<style lang="less">
.settings-modal-shell.agent-editor-shell {
  .section--prompts {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .prompts-panel {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .prompts-panel__header {
    flex-shrink: 0;
    margin: 0;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);
  }

  .prompts-panel__body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    margin: 0 -4px;
    padding: 4px 4px 8px;
  }

  .prompts-panel__pane.setting-row:last-child,
  .prompts-panel__pane--stack .setting-row:last-child {
    border-bottom: none;
  }

  .prompts-outline {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 14px;
    min-width: 0;

    &__pill {
      display: inline-flex;
      align-items: center;
      gap: 5px;
      padding: 5px 12px;
      border: none;
      border-radius: var(--app-radius-sm);
      background: var(--td-bg-color-secondarycontainer);
      font: inherit;
      font-size: var(--app-text-md);
      line-height: 1.4;
      color: var(--td-text-color-secondary);
      cursor: pointer;
      transition: color var(--app-motion-fast) ease, background var(--app-motion-fast) ease;

      &:hover,
      &:focus-visible {
        color: var(--td-brand-color);
        background: color-mix(in oklab, var(--td-brand-color) 8%, var(--td-bg-color-secondarycontainer));
        outline: none;
      }

      &--active {
        background: color-mix(in oklab, var(--td-brand-color) 12%, transparent);
        color: var(--td-brand-color);
        font-weight: 500;
      }
    }

    &__dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
      background: var(--td-brand-color);
      flex-shrink: 0;
    }
  }

  .textarea-with-template {
    width: 100%;

    .textarea-actions {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
      min-height: 32px;
      margin-top: 4px;
      padding: 2px 0;

      &--right {
        justify-content: flex-end;
      }
    }

    .prompt-assistant-action {
      flex-shrink: 0;
    }

    .prompt-template-selector {
      flex-shrink: 0;
      max-width: 100%;
      margin-inline-start: auto;
    }

    .template-btn-group {
      flex-wrap: wrap;
      justify-content: flex-end;
    }
  }

  .intent-prompts-editor {
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .intent-toggle-group {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  .intent-toggle-group .intent-toggle-btn--active {
    background-color: color-mix(in oklab, var(--td-brand-color) 10%, transparent);
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
    font-weight: 500;

    &:hover,
    &:focus-visible {
      background-color: color-mix(in oklab, var(--td-brand-color) 14%, transparent);
      border-color: var(--td-brand-color);
      color: var(--td-brand-color);
    }
  }

  .intent-toggle-btn {
    max-width: 100%;
  }

  .intent-toggle-label {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    white-space: nowrap;
  }

  .intent-toggle-dot {
    flex-shrink: 0;
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: currentColor;
  }

  .intent-active-desc {
    margin: 0;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
    line-height: 1.5;
  }

  .system-prompt-textarea {
    width: 100%;
    font-family: var(--app-font-family-mono);
    font-size: var(--app-text-md);

    textarea {
      resize: vertical !important;
      min-height: 200px;
    }
  }

  .placeholder-tags {
    margin-top: 6px;
    display: flex;
    align-items: center;
    gap: 5px;
    font-size: var(--app-text-sm);
    line-height: 1.4;
    overflow-x: auto;
    white-space: nowrap;
    padding-bottom: 4px;
    scrollbar-width: thin;
    scrollbar-color: var(--gy-scrollbar-thumb) var(--gy-scrollbar-track);

    .placeholder-label {
      color: var(--td-text-color-secondary);
      flex-shrink: 0;
    }

    .placeholder-hint {
      color: var(--td-text-color-placeholder);
      font-size: var(--app-text-xs);
      user-select: none;
      flex-shrink: 0;
    }

    .placeholder-tag {
      display: inline-flex;
      align-items: center;
      padding: 1px 5px;
      border-radius: var(--gy-radius-xs);
      font-family: var(--app-font-family-mono);
      font-size: var(--app-text-xs);
      color: var(--td-text-color-primary);
      background-color: var(--td-bg-color-secondarycontainer);
      cursor: pointer;
      transition: color var(--gy-motion-base) var(--gy-ease-default),
        background-color var(--gy-motion-base) var(--gy-ease-default),
        border-color var(--gy-motion-base) var(--gy-ease-default);
      user-select: none;
      border: 1px solid transparent;
      flex-shrink: 0;

      &:hover {
        color: var(--td-brand-color);
        background-color: var(--td-brand-color-light);
        border-color: var(--td-brand-color-focus);
      }

      &:active {
        background-color: var(--td-brand-color-focus);
      }

      &:focus-visible {
        outline: 2px solid var(--td-brand-color-focus);
        outline-offset: 1px;
      }
    }
  }

  @supports not (scrollbar-color: auto) {
    .placeholder-tags {
      scrollbar-width: auto;
    }

    .placeholder-tags::-webkit-scrollbar {
      height: 4px;
    }

    .placeholder-tags::-webkit-scrollbar-thumb {
      background: var(--gy-scrollbar-thumb);
      border-radius: var(--gy-radius-xs);
    }
  }

  .prompt-disabled-hint {
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-md);
    font-style: italic;
    padding: 12px 16px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: var(--app-radius-sm);
  }
}

.placeholder-popup-wrapper {
  position: fixed;
  z-index: 10001;
  pointer-events: auto;
}

.placeholder-popup {
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  box-shadow: var(--gy-shadow-md);
  max-width: 320px;
  max-height: 240px;
  overflow-y: auto;
  padding: 4px;
}

.placeholder-item {
  padding: 6px 10px;
  cursor: pointer;
  transition: background-color var(--app-motion-fast);
  border-radius: var(--app-radius-xs);

  &:hover,
  &.active {
    background-color: var(--td-bg-color-container-hover);
  }

  .placeholder-name {
    margin-bottom: 2px;

    code {
      background: var(--td-bg-color-container-hover);
      padding: 2px 5px;
      border-radius: var(--gy-radius-xs);
      font-family: var(--app-font-family-mono);
      font-size: var(--app-text-xs);
      color: var(--td-brand-color);
    }
  }

  .placeholder-desc {
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
  }
}
</style>
